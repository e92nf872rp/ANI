"""
E2E test for issue-024 (B6) — Gateway RebuildKB endpoint.

NEW endpoint under test (SPEC §4.3, B6 / plan §2.7):
  POST /knowledge-bases/{kb_id}/rebuild (body: idempotency_key)

Test groups:
  T1  RebuildKB happy path (contract + DB artifacts):
      active KB -> 202 AsyncTaskRef{task_id, task_type="kb.rebuild",
      status="pending"}; DB rows in the SAME atomic outbox transaction:
      knowledge_bases active->rebuilding / async_tasks(pending,
      payload={kb_id, embedding_model, chunk_size}, resource_type=
      'knowledge_base', resource_id=kb_id) / kb_audit_log(action='kb.rebuild',
      before={"status":"active"}, after={"status":"rebuilding"}) /
      outbox_events(event_type='kb.rebuild', aggregate_type=
      'knowledge_bases', aggregate_id=kb_id, payload={kb_id, tenant_id,
      task_id}).
  T2  uuid validation negatives (gateway-side, pre-gRPC):
      non-uuid key -> 400 "idempotency_key must be a uuid"
      empty key    -> 400 "idempotency_key is required"
  T3  Guard: missing KB -> 404 "knowledge base not found".
  T4  Guard: KB already rebuilding (natural state after T1 — no consumer
      in Mode B) -> 409 "knowledge base is rebuilding or not active".
  T5  Idempotent replay: same uuid key re-posted while task pending ->
      202, same task_id, NO new async_tasks row, NO new outbox event.
  T6  Poison-key path (SPEC §5.4): a prior FAILED kb.rebuild task with the
      same key -> 409 "task already failed with this idempotency_key; retry
      with a new key"; the abort rolls back the WHOLE outer transaction
      including the rebuilding transition (KB stays active) and inserts no
      new outbox event.
  T7  Cross-tenant negative: rebuild on another tenant's KB -> 404.
  T8  Foreign task_type probe: a kb.parse task row holding the SAME
      (tenant, uuid key) is NOT replayed by rebuild (task_type filter);
      UNIQUE(tenant_id, idempotency_key) then rejects the INSERT ->
      409 poison / 500 both prove the filter skipped the foreign row.

Seed data goes DIRECTLY to PG (asyncpg, RLS-scoped) — failed/ready docs
which the upload pipeline does not produce on demand. In Mode B the
local rebuild consumer is disabled, so nothing local consumes the event;
the SERVER-deployed kb-service sharing the dev DB still polls outbox_events
every 1s — _OutboxGuardThread shields the T1 event from it (marks it
published within ~10-50ms of commit). In Mode A (consumer ON) the guard is
NOT armed: the local consumer must be allowed to pick the event up (kbnew
precedent).

Services (local only, never uploaded to the server):
  - ani-gateway (Go, :8080)      repo/bin/ani-gateway.exe
  - kb-service  (Python, :8002 / gRPC :50053)
  - rag-engine  (Python, :8001 / gRPC :50052)  — Mode A only

Infrastructure (server-deployed NodePorts on 10.10.1.66):
  PostgreSQL :30945 / MinIO :30900 / NATS :31062 / Redis :30453 / Milvus :31930

Usage:
  python tests/e2e/test_kb_rebuild_e2e.py            # Mode B (default)
  python tests/e2e/test_kb_rebuild_e2e.py --mode-a  # full chain, consumer ON
"""
from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error
import uuid
from datetime import datetime
from pathlib import Path
from typing import Any

# ── paths ────────────────────────────────────────────────────────────────────
REPO = Path(__file__).resolve().parents[4]  # repo/
KB_SVC = REPO / "services" / "kb-service"
GATEWAY_EXE = REPO / "bin" / "ani-gateway.exe"
E2E_LOG = KB_SVC / "tests" / "e2e" / "e2e_kb_rebuild_result.log"

# make app.repositories (production helpers) importable
sys.path.insert(0, str(KB_SVC))

# ── server component endpoints (NodePorts on 10.10.1.66) ─────────────────────
SRV = "10.10.1.66"
PG_URL = f"postgres://ani_app_user:ani_dev_password@{SRV}:30945/ani?sslmode=disable"
MILVUS_ADDR = f"{SRV}:31930"
MINIO_ENDPOINT = f"{SRV}:30900"
MINIO_AK = "ani-s05-minio"
MINIO_SK = "F36UCbnRR-bY9Upv8uuammuBwkHFlTYABiXCbtMCmlc"
NATS_URL = f"nats://{SRV}:31062"
REDIS_URL = f"redis://:ani_dev_password@{SRV}:30453/0"

VLLM_MODEL = "Qwen3-235B-A22B"
VLLM_API_BASE = "http://10.10.20.181:3011/v1"
VLLM_API_KEY = "sk-YOp8k71BXjxBTeZniPPvQlbGgciH0CB9WOWXkmuCzjfIZ5L8"

# Mode A embedding (rag-engine real re-parse through SiliconFlow)
EMBEDDING_MODEL = "BAAI/bge-m3"
EMBEDDING_API_BASE = "https://api.siliconflow.cn/v1"
EMBEDDING_API_KEY = "sk-fdckgjrqrzfmjrscxkzuppxsfhijborgiwqjwbamdgcyitwc"

TENANT_ID = "00000000-0000-0000-0000-000000000002"
OTHER_TENANT_ID = "00000000-0000-0000-0000-000000000003"
E2E_TAG = f"kbrebuild-{int(time.time())}"

REST_BASE = "http://localhost:8080/api/v1/svc"
REST_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": TENANT_ID}
OTHER_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": OTHER_TENANT_ID}

# ── mode ─────────────────────────────────────────────────────────────────────
# Mode B (default): contract + DB artifacts only — local rebuild consumer OFF,
#   guard armed for T1 (server-dispatcher shield).
# Mode A: full chain — rag-engine + parse consumer + rebuild consumer ON,
#   guard NOT armed (kbnew precedent: the local consumers read outbox_events
#   directly, so no server-dispatcher shield is needed).
MODE_A = "--mode-a" in sys.argv

# unique NATS subjects per run (server consumers listen on the defaults).
# V2 matters only in Mode A: with the parse consumer ON the outbox
# dispatcher publishes kb.parse events to nats_parse_subject_v2, which
# defaults to the shared server subject and MUST be overridden too
# (kbnew precedent).
LOCAL_PARSE_SUBJECT = f"ani.tasks.kb.parse.{E2E_TAG}"
LOCAL_PARSE_SUBJECT_V2 = f"ani.tasks.kb.parse.{E2E_TAG}.v2"
LOCAL_REBUILD_SUBJECT = f"ani.tasks.kb.rebuild.{E2E_TAG}"

# ── logging (terminal + file) ────────────────────────────────────────────────
_log_fh: io.TextIOBase | None = None


def _open_log():
    global _log_fh
    E2E_LOG.parent.mkdir(parents=True, exist_ok=True)
    _log_fh = open(E2E_LOG, "w", encoding="utf-8")


def log(msg: str = "", **kw):
    ts = datetime.now().strftime("%H:%M:%S")
    line = f"[{ts}] {msg}" if msg else ""
    print(line, flush=True, **kw)
    if _log_fh:
        _log_fh.write(line + "\n")
        _log_fh.flush()


def log_json(label: str, data):
    log(f"── {label} ──")
    try:
        text = json.dumps(data, ensure_ascii=False, indent=2, default=str)
    except Exception:
        text = str(data)
    print(text, flush=True)
    if _log_fh:
        _log_fh.write(text + "\n")
        _log_fh.flush()


# ── process management ───────────────────────────────────────────────────────
_procs: list[subprocess.Popen] = []


def _start(args: list[str], env: dict, cwd: Path, name: str) -> subprocess.Popen:
    full_env = os.environ.copy()
    full_env.update(env)
    log(f"Starting {name}: {' '.join(args[:3])}... (cwd={cwd})")
    log_file = open(E2E_LOG.parent / f"{name}_kbrebuild.stdout.log", "w",
                    encoding="utf-8")
    p = subprocess.Popen(
        args,
        env=full_env,
        cwd=str(cwd),
        stdout=log_file,
        stderr=subprocess.STDOUT,
        creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0,
    )
    _procs.append(p)
    log(f"  {name} started: pid={p.pid}")
    return p


def _wait_http(url: str, timeout: float = 60.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=3)
            log(f"  ready at {url}")
            return True
        except urllib.error.HTTPError:
            log(f"  ready (HTTP error response = server up) at {url}")
            return True
        except Exception:
            time.sleep(1.0)
    log(f"  NOT ready at {url} (timeout {timeout}s)")
    return False


def _kill_all():
    for p in reversed(_procs):
        try:
            p.terminate()
            p.wait(timeout=5)
        except Exception:
            try:
                p.kill()
            except Exception:
                pass


def _kill_stale_processes():
    """Kill any previously-started local service processes on our ports."""
    names = ["ani-gateway", "kb-service", "rag-engine", "uvicorn"]
    for n in names:
        try:
            subprocess.run(
                ["taskkill", "/F", "/IM", f"{n}.exe"],
                capture_output=True, timeout=10,
            )
        except Exception:
            pass
    for port in (8001, 8002, 50052, 50053, 8080):
        try:
            out = subprocess.run(
                ["netstat", "-ano", "-p", "tcp"],
                capture_output=True, text=True, timeout=10,
            ).stdout
            for line in out.splitlines():
                if f":{port}" in line and "LISTENING" in line:
                    pid = line.strip().split()[-1]
                    subprocess.run(["taskkill", "/F", "/PID", pid],
                                   capture_output=True, timeout=10)
        except Exception:
            pass
    time.sleep(1.0)


# ── service environments ─────────────────────────────────────────────────────
def _gateway_env():
    env = os.environ.copy()
    env.update({
        "ANI_AUTH_MODE": "dev",
        "GATEWAY_LISTEN_ADDR": ":8080",
        "DATABASE_URL": PG_URL,
        "REDIS_URL": REDIS_URL,
        "OBJECT_STORE_PROVIDER": "minio",
        "OBJECT_STORE_ENDPOINT": MINIO_ENDPOINT,
        "OBJECT_STORE_PUBLIC_ENDPOINT": MINIO_ENDPOINT,
        "OBJECT_STORE_ACCESS_KEY_ID": MINIO_AK,
        "OBJECT_STORE_SECRET_ACCESS_KEY": MINIO_SK,
        "OBJECT_STORE_SECURE": "false",
        "OBJECT_STORE_BUCKET_PREFIX": "ani-s13-",
        "OBJECT_STORE_REGION": "us-east-1",
        "VECTOR_STORE_PROVIDER": "milvus",
        "VECTOR_STORE_ENDPOINT": f"http://{MILVUS_ADDR}",
        "VECTOR_STORE_COLLECTION_PREFIX": "ani_s13_",
        "NATS_URL": NATS_URL,
        "STORAGE_PROVIDER": "",
        "K8S_CLUSTER_PROVIDER_MODE": "",
        "GPU_INVENTORY_PROVIDER": "",
        "NETWORK_PROVIDER": "",
        "REGISTRY_PROVIDER_MODE": "",
        "INSTANCE_OBSERVABILITY_PROVIDER": "",
        "KB_SERVICE_GRPC_ADDR": "localhost:50053",
        "KB_SERVICE_GRPC_CALL_TIMEOUT": "30s",
        "RAG_ENGINE_URL": "http://localhost:8001",
        "VLLM_API_BASE": VLLM_API_BASE,
        "VLLM_API_KEY": VLLM_API_KEY,
        "VLLM_MODEL": VLLM_MODEL,
    })
    return env


def _kb_service_env():
    env = os.environ.copy()
    env.update({
        "DATABASE_URL": PG_URL,
        "NATS_URL": NATS_URL,
        "NATS_PARSE_SUBJECT": LOCAL_PARSE_SUBJECT,
        "NATS_PARSE_SUBJECT_V2": LOCAL_PARSE_SUBJECT_V2,
        "NATS_REBUILD_SUBJECT": LOCAL_REBUILD_SUBJECT,
        "ANI_GATEWAY_INTERNAL_URL": "http://localhost:8080",
        "RAG_ENGINE_GRPC_ADDR": "localhost:50052",
        "GRPC_PORT": "50053",
        "REDIS_URL": REDIS_URL,
        # Mode A turns BOTH local consumers ON (full chain): the
        # notify-uploaded kb.parse outbox event must be consumed locally
        # so the real doc actually gets parsed+embedded (rag-engine is a
        # stateless gRPC service with NO NATS consumer of its own — the
        # kb-service parse consumer is the only local parse path).
        # Mode B keeps both OFF (contract + DB artifacts only; T1 leaves
        # the KB in 'rebuilding' until the cleanup resets it).
        "KB_PARSE_CONSUMER_ENABLED": "true" if MODE_A else "false",
        "KB_REBUILD_CONSUMER_ENABLED": "true" if MODE_A else "false",
        "ANI_DEV_TENANT_ID": TENANT_ID,
    })
    return env


def _rag_engine_env():
    env = os.environ.copy()
    # kb-service config uses ANI_GATEWAY_INTERNAL_URL; rag-engine's alias set
    # accepts ANI_GATEWAY_URL — pop the former so the latter takes effect.
    env.pop("ANI_GATEWAY_INTERNAL_URL", None)
    env.update({
        "MILVUS_ADDR": MILVUS_ADDR,
        "DATABASE_URL": PG_URL,
        "NATS_URL": NATS_URL,
        "NATS_PARSE_SUBJECT": LOCAL_PARSE_SUBJECT,
        "EMBEDDING_MODEL": EMBEDDING_MODEL,
        "EMBEDDING_API_BASE": EMBEDDING_API_BASE,
        "EMBEDDING_API_KEY": EMBEDDING_API_KEY,
        "MINIO_ENDPOINT": MINIO_ENDPOINT,
        "MINIO_ACCESS_KEY": MINIO_AK,
        "MINIO_SECRET_KEY": MINIO_SK,
        "MINIO_SECURE": "false",
        "MINIO_BUCKET": "ani-kb-docs",
        "VLLM_MODEL": VLLM_MODEL,
        "VLLM_API_BASE": VLLM_API_BASE,
        "VLLM_API_KEY": VLLM_API_KEY,
        "VLLM_CONTEXT_WINDOW": "32768",
        "REDIS_URL": REDIS_URL,
        "ANI_GATEWAY_URL": "http://localhost:8080",
        "ANI_DEV_TENANT_ID": TENANT_ID,
    })
    return env


def start_gateway():
    _start([str(GATEWAY_EXE)], _gateway_env(),
           REPO / "services" / "ani-gateway", "gateway")


def start_kb_service():
    _start([sys.executable, "main.py"], _kb_service_env(), KB_SVC, "kb-service")


def start_rag_engine():
    # rag-engine has its OWN venv (openai/pymilvus/minio) — the kb-service
    # venv lacks the openai package and dies at startup on init_embedding_model.
    rag_py = REPO / "ai" / "rag-engine" / ".venv" / "Scripts" / "python.exe"
    if not rag_py.exists():
        log(f"FATAL: rag-engine venv python not found at {rag_py}")
        raise SystemExit(1)
    _start([str(rag_py), "main.py"], _rag_engine_env(),
           REPO / "ai" / "rag-engine", "rag-engine")


# ── REST helpers ──────────────────────────────────────────────────────────────
def rest_request(method: str, path: str, body: dict | None = None,
                 expected_status: int | None = None, label: str = "",
                 timeout: float = 60.0, base: str | None = None,
                 headers: dict | None = None, desc: str = ""):
    """Send a REST request through the gateway; log the interface purpose
    (desc), input and output. Returns (status_code, parsed_body)."""
    url = (base or REST_BASE) + path
    hdrs = dict(headers or REST_HDRS)
    data = None
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    if desc:
        log(f"[{label or method}] 接口作用: {desc}")
    req = urllib.request.Request(url, data=data, method=method.upper(), headers=hdrs)
    log(f"[{label or method + ' ' + path}] INPUT: {method} {url}")
    if body is not None:
        log_json(f"[{label}] REQUEST BODY", body)
    status = -1
    payload: Any = None
    try:
        resp = urllib.request.urlopen(req, timeout=timeout)
        status = resp.status
        raw = resp.read().decode("utf-8", errors="replace")
        if raw:
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = raw
    except urllib.error.HTTPError as e:
        status = e.code
        raw = e.read().decode("utf-8", errors="replace") if e.fp else ""
        if raw:
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = raw
    except Exception as e:
        log(f"[{label}] REQUEST ERROR: {e!r}")
        return -1, {"error": repr(e)}
    log(f"[{label}] OUTPUT: HTTP {status}")
    log_json(f"[{label}] RESPONSE BODY", payload)
    if expected_status is not None and status != expected_status:
        log(f"[{label}] ✗ UNEXPECTED STATUS: expected={expected_status} actual={status}")
    return status, payload


# ── check helpers ─────────────────────────────────────────────────────────────
RESULTS: dict[str, str] = {}  # label -> "pass" | "fail"


def check(label: str, ok: bool, detail: str = ""):
    RESULTS[label] = "pass" if ok else "fail"
    mark = "✓" if ok else "✗"
    log(f"  {mark} {label}" + (f" — {detail}" if detail else ""))


def _msg_of(payload: Any) -> str:
    if isinstance(payload, dict):
        return str(payload.get("message", ""))
    return str(payload or "")


# ── seed/query helpers (direct PG, RLS-scoped) ───────────────────────────────
def _run_async(coro) -> Any:
    import asyncio
    return asyncio.run(coro)


async def _ensure_tenant_rows():
    """Ensure tenant rows for TENANT_ID and OTHER_TENANT_ID exist (FK source)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        for tid, name in ((TENANT_ID, "kbrebuild-dev-tenant"),
                          (OTHER_TENANT_ID, "kbrebuild-other-tenant")):
            await conn.execute(
                "INSERT INTO tenants (id, name, display_name, plan_id) "
                "VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING",
                uuid.UUID(tid), name, name,
                uuid.UUID("00000000-0000-0000-0000-000000000001"))
        log(f"  tenants ensured: {TENANT_ID} / {OTHER_TENANT_ID}")
    finally:
        await conn.close()


async def _insert_doc(kb_id: str, name: str, status: str,
                      error: str | None) -> str:
    """Insert one kb_documents row (own tx, RLS tenant context)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            # SET LOCAL cannot use $1 binding — inline the UUID literal.
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                INSERT INTO kb_documents
                    (kb_id, tenant_id, file_name, file_type,
                     file_size_bytes, storage_path, checksum_sha256,
                     parse_status, error_message)
                VALUES ($1, $2, $3, 'md', 100, $4, $5, $6, $7)
                RETURNING id::text
                """,
                uuid.UUID(kb_id), uuid.UUID(TENANT_ID), name,
                f"{E2E_TAG}/{name}", "0" * 64, status, error)
            return row["id"]
    finally:
        await conn.close()


async def _set_kb_status(kb_id: str, status: str) -> bool:
    """Force a knowledge_bases.status value (test-only, RLS-scoped)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            res = await conn.execute(
                "UPDATE knowledge_bases SET status=$2 WHERE id=$1",
                uuid.UUID(kb_id), status)
            return res == "UPDATE 1"
    finally:
        await conn.close()


async def _get_kb_status(kb_id: str) -> str | None:
    """Read knowledge_bases.status (RLS-scoped)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                "SELECT status FROM knowledge_bases WHERE id=$1",
                uuid.UUID(kb_id))
            return row["status"] if row else None
    finally:
        await conn.close()


async def _seed_task(idem_key: str, task_type: str, status: str,
                     resource_id: str | None = None,
                     payload: dict | None = None) -> str:
    """Insert an async_tasks row with a GIVEN task_type/status under the
    same tenant (task_type-filter / poison-key probes). Returns the id."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                INSERT INTO async_tasks
                    (tenant_id, idempotency_key, task_type, resource_type,
                     resource_id, status, payload)
                VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
                RETURNING id::text
                """,
                uuid.UUID(TENANT_ID), idem_key, task_type,
                "knowledge_base" if resource_id else "kb_document",
                uuid.UUID(resource_id) if resource_id else None,
                status, json.dumps(payload or {}))
            return row["id"]
    finally:
        await conn.close()


async def _fetch_task(task_id: str) -> dict | None:
    """Read an async_tasks row by id (RLS tenant context required)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT id::text, task_type, status, idempotency_key,
                       resource_type, resource_id::text, payload,
                       progress_pct, result, error_message
                  FROM async_tasks WHERE id = $1
                """,
                uuid.UUID(task_id))
            return dict(row) if row else None
    finally:
        await conn.close()


async def _fetch_kb_audit(kb_id: str, action: str) -> dict | None:
    """Read the latest kb_audit_log row for (kb_id, action) — the index
    (tenant_id, kb_id, created_at DESC, id DESC) supports this ordering."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT action, actor_user_id::text, before_state, after_state,
                       error_code, error_msg
                  FROM kb_audit_log
                 WHERE kb_id = $1 AND action = $2
                 ORDER BY created_at DESC, id DESC
                 LIMIT 1
                """,
                uuid.UUID(kb_id), action)
            return dict(row) if row else None
    finally:
        await conn.close()


async def _fetch_outbox_event(kb_id: str) -> dict | None:
    """Read the latest kb.rebuild outbox event for a KB (RLS-scoped)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT id, event_type, aggregate_type, aggregate_id::text,
                       payload, published
                  FROM outbox_events
                 WHERE aggregate_id = $1 AND event_type = 'kb.rebuild'
                 ORDER BY id DESC LIMIT 1
                """,
                uuid.UUID(kb_id))
            return dict(row) if row else None
    finally:
        await conn.close()


async def _count_outbox_events(kb_id: str, event_type: str) -> int:
    """Count outbox events of a type for a KB (RLS-scoped)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            n = await conn.fetchval(
                """
                SELECT COUNT(*) FROM outbox_events
                 WHERE aggregate_id = $1 AND event_type = $2
                """,
                uuid.UUID(kb_id), event_type)
            return int(n)
    finally:
        await conn.close()


async def _count_tasks_by_key(idem_key: str) -> int:
    """Count async_tasks rows for one (tenant, key) — replay must not add."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            n = await conn.fetchval(
                """
                SELECT COUNT(*) FROM async_tasks
                 WHERE tenant_id = $1 AND idempotency_key = $2
                """,
                uuid.UUID(TENANT_ID), idem_key)
            return int(n)
    finally:
        await conn.close()


def _payload_of(raw: Any) -> dict:
    """outbox/audit JSONB columns come back as str or dict — normalize."""
    if isinstance(raw, str):
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {}
    return raw or {}


class _OutboxGuardThread:
    """Pre-connected watchdog that shields a kb.rebuild outbox event from
    the SERVER-deployed kb-service sharing the dev DB.

    Threat model (issue-048 precedent, observed live): the server-side
    OutboxDispatcher polls outbox_events (BYPASSRLS, every 1s, batch 100)
    for published=FALSE rows. If it snatches OUR e2e event it publishes it
    to the SERVER's rebuild subject where a stale server consumer would
    process the KB against server-side stores.

    Why a dedicated thread with a pre-established connection: a naive
    post-202 `asyncpg.connect()` costs ~300ms (TCP+auth+RTT) — vs a 1s
    poll that's a ~30% collision window. Pre-connecting BEFORE the rebuild
    request and then tight-polling (≤10ms + RTT) shrinks the window to
    ~50ms (~5%). asyncpg connections bind to the event loop that created
    them, and _run_async() runs a fresh loop per call, so the guard must
    own a dedicated thread + loop + persistent connection.

    The ani_app role has UPDATE (not DELETE) on outbox_events (audit-table
    design) — mark_dispatched uses exactly this UPDATE shape, so we mirror
    production's own transition. In Mode A the guard is NOT armed: the
    local rebuild consumer also reads outbox_events directly (same DB) and
    the guard would block it (kbnew precedent).
    """

    def __init__(self, kb_id: str):
        self._kb_id = kb_id
        self._stop = threading.Event()
        self._ready = threading.Event()   # set once PG conn + tenant GUC done
        self._done = threading.Event()     # set once event marked / gave up
        self._thread: threading.Thread | None = None
        self.result: str = ""          # final UPDATE result ("UPDATE N")
        self.latency_ms: float | None = None  # arm → shield applied
        self.error: str = ""

    # ---- thread body -----------------------------------------------------
    def _run(self):
        import asyncio
        import asyncpg

        async def guard_loop():
            conn = await asyncpg.connect(PG_URL)
            try:
                # session-level tenant GUC (NOT SET LOCAL): persists across
                # the tight-poll transactions on this one connection.
                await conn.execute(
                    f"SET app.current_tenant_id = '{TENANT_ID}'")
                self._ready.set()
                sent_at = time.monotonic()
                deadline = sent_at + 30.0
                while not self._stop.is_set() and time.monotonic() < deadline:
                    async with conn.transaction():
                        rid = await conn.fetchval(
                            """
                            SELECT id FROM outbox_events
                             WHERE aggregate_id = $1
                               AND event_type = 'kb.rebuild'
                               AND published = FALSE
                            """,
                            uuid.UUID(self._kb_id))
                        if rid is not None:
                            res = await conn.execute(
                                """
                                UPDATE outbox_events
                                   SET published = TRUE, published_at = now()
                                 WHERE id = $1 AND published = FALSE
                                """,
                                rid)
                            self.result = str(res)
                            self.latency_ms = (
                                time.monotonic() - sent_at) * 1000.0
                            return
                    await asyncio.sleep(0.01)  # 10ms tight poll
            finally:
                self._done.set()
                await conn.close()

        try:
            asyncio.run(guard_loop())
        except Exception as e:  # surface to caller via .error
            self.error = repr(e)
            self._ready.set()
            self._done.set()

    # ---- lifecycle ------------------------------------------------------
    def start(self, connect_timeout: float = 5.0) -> bool:
        """Pre-connect and arm the poll loop (call BEFORE the rebuild
        request; the loop only marks rows that already exist). Returns True
        once the PG connection and tenant GUC are armed — no blind sleeps."""
        self._thread = threading.Thread(
            target=self._run, name=f"outbox-guard-{self._kb_id[:8]}",
            daemon=True)
        self._thread.start()
        if not self._ready.wait(connect_timeout):
            log("  outbox guard: PG connect did not become ready in time")
            self._stop.set()
            return False
        if self.error:
            log(f"  outbox guard: armed WITH error: {self.error}")
            return False
        return True

    def finish(self, timeout: float = 3.0) -> bool:
        """Wait (bounded) for the guard to finish its marking pass — do NOT
        pre-empt with stop(): the 202 may return before the guard's next
        10ms poll spin observes the freshly committed event. On a non-202
        (no event ever created) this burns at most `timeout`."""
        if self._thread and self._thread.is_alive():
            self._done.wait(timeout)
        return self.stop(2.0)

    def stop(self, timeout: float = 5.0) -> bool:
        self._stop.set()
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout)
        if self._thread and self._thread.is_alive():
            log(f"  outbox guard thread did not exit within {timeout}s")
            return False
        return True


# ── NATS durable hygiene ─────────────────────────────────────────────────────
# kb-service consumers use FIXED durable names (kb-parse-consumer /
# kb-rebuild-consumer) but per-run unique subjects. nats-py does NOT detect
# filter_subject drift on an existing durable (upstream TODO): a leftover
# durable from an aborted run binds the new subscription to the OLD subject
# and the local consumers silently receive nothing. So: pre-delete any
# leftover e2e durables before starting kb-service, and clean them again
# in the finally block. Server-owned durables (e.g. model-import-worker)
# are never touched.
NATS_STREAM = "ANI_TASKS"
E2E_DURABLES = ("kb-parse-consumer", "kb-rebuild-consumer")


def _nats_purge_e2e_durables(when: str) -> None:
    import nats

    async def _purge():
        nc = await nats.connect(NATS_URL)
        try:
            jsm = nc.jsm()
            for name in E2E_DURABLES:
                try:
                    info = await jsm.consumer_info(NATS_STREAM, name)
                except Exception:  # noqa: BLE001 — not found: nothing to do
                    continue
                log(f"  [{when}] found leftover durable {name} "
                    f"(filter={info.config.filter_subject}) — deleting")
                await jsm.delete_consumer(NATS_STREAM, name)
        finally:
            await nc.drain()

    try:
        _run_async(_purge())
    except Exception as e:  # noqa: BLE001
        log(f"  [{when}] durable purge failed: {e!r}")


# ── Mode A helpers ───────────────────────────────────────────────────────────
def _doc_md_bytes() -> bytes:
    """Small markdown doc for the real upload path (Mode A)."""
    md = f"""# ANI 知识库重建测试文档 {E2E_TAG}

## 一、平台概述
ANI 平台知识库支持文档上传、异步解析、向量化与多模式检索。

## 二、重建语义
知识库重建（rebuild）对全部 ready/failed 文档逐个重新解析与向量化，
重建期间知识库进入 rebuilding 状态，重建完成后回到 active。
"""
    return md.encode("utf-8")


def http_put_bytes(url: str, data: bytes, label: str) -> int:
    """PUT raw bytes to a presigned URL (MinIO object upload)."""
    req = urllib.request.Request(url, data=data, method="PUT")
    try:
        resp = urllib.request.urlopen(req, timeout=60)
        log(f"  [{label}] PUT -> {resp.status}")
        return resp.status
    except urllib.error.HTTPError as e:
        log(f"  [{label}] PUT HTTP {e.code}: {e.read()[:200]!r}")
        return e.code
    except Exception as e:
        log(f"  [{label}] PUT ERROR: {e!r}")
        return -1


async def _fetch_doc_state(doc_id: str) -> dict | None:
    """Read the doc's parse_status / error_message / chunk_count."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT parse_status, error_message, chunk_count
                  FROM kb_documents WHERE id = $1
                """,
                uuid.UUID(doc_id))
            return dict(row) if row else None
    finally:
        await conn.close()


async def _set_doc_status(doc_id: str, status: str, error: str | None):
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            await conn.execute(
                "UPDATE kb_documents SET parse_status=$2, error_message=$3 "
                "WHERE id=$1",
                uuid.UUID(doc_id), status, error)
    finally:
        await conn.close()


def wait_doc_ready(doc_id: str, timeout: float = 240) -> dict:
    """Poll kb_documents.parse_status until ready/failed (asyncpg)."""
    deadline = time.time() + timeout
    state = {"parse_status": "unknown"}
    while time.time() < deadline:
        state = _run_async(_fetch_doc_state(doc_id)) or state
        if state.get("parse_status") in ("ready", "failed"):
            return state
        time.sleep(2.0)
    return state


def wait_task_terminal(task_id: str, timeout: float = 600) -> dict | None:
    """Poll the async_tasks row until completed/failed (Mode A)."""
    deadline = time.time() + timeout
    row = None
    while time.time() < deadline:
        row = _run_async(_fetch_task(task_id))
        if row and row.get("status") in ("completed", "failed"):
            return row
        time.sleep(3.0)
    return row


# ── cleanup ──────────────────────────────────────────────────────────────────
async def _purge_seeded_rows(kb_ids: list[str], doc_ids: list[str]):
    """Physically purge seeded rows (DeleteKB is a SOFT delete; pure e2e
    test data must not linger in the shared dev DB). FK-safe order:
    kb_messages -> kb_sessions -> kb_chunks -> kb_documents."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        for kb_id in kb_ids:
            async with conn.transaction():
                await conn.execute(
                    f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
                await conn.execute(
                    "DELETE FROM kb_messages WHERE session_id IN "
                    "(SELECT id FROM kb_sessions WHERE kb_id=$1)",
                    uuid.UUID(kb_id))
                await conn.execute(
                    "DELETE FROM kb_sessions WHERE kb_id=$1", uuid.UUID(kb_id))
                await conn.execute(
                    "DELETE FROM kb_chunks WHERE kb_id=$1", uuid.UUID(kb_id))
                await conn.execute(
                    "DELETE FROM kb_documents WHERE kb_id=$1", uuid.UUID(kb_id))
                n = await conn.fetchval(
                    "SELECT COUNT(*) FROM kb_documents WHERE kb_id=$1",
                    uuid.UUID(kb_id))
                log(f"  purged docs for kb {kb_id} (remaining: {n})")
    finally:
        await conn.close()


async def _purge_task_and_outbox_rows(idem_keys: list[str], kb_ids: list[str]):
    """Neutralize the async_tasks / outbox_events rows this run created.

    The ani_app role only has SELECT/INSERT/UPDATE on these two tables
    (audit-table design, migration 20260828000200) — DELETE is denied.
    - outbox_events: UPDATE published=TRUE so the server-deployed
      dispatcher never sees the rows (side effects neutralized; rows stay
      as audit trail, which is the intended design).
    - async_tasks: attempt DELETE, degrade gracefully on permission
      denial — rows are harmless (each run uses fresh uuid4 keys, no
      cross-run interference) and audits keep them by design."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            res = await conn.execute(
                """
                UPDATE outbox_events
                   SET published = TRUE, published_at = now()
                 WHERE tenant_id=$1 AND aggregate_id = ANY($2::uuid[])
                   AND event_type = 'kb.rebuild' AND published = FALSE
                """,
                uuid.UUID(TENANT_ID), [uuid.UUID(k) for k in kb_ids])
            log(f"  neutralize outbox_events: {res}")
        try:
            async with conn.transaction():
                await conn.execute(
                    f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
                res = await conn.execute(
                    """
                    DELETE FROM async_tasks
                     WHERE tenant_id=$1 AND idempotency_key = ANY($2::text[])
                    """,
                    uuid.UUID(TENANT_ID), idem_keys)
                log(f"  purge async_tasks: {res}")
        except Exception as e:
            log(f"  async_tasks DELETE denied (audit table, expected on "
                f"shared dev DB): {e!r}")
    finally:
        await conn.close()


# ── run_e2e ───────────────────────────────────────────────────────────────────
def run_e2e():
    idem_keys: list[str] = []

    # ══ Precondition: CreateKB kb1 (rebuild target; real vector_store_id) ══
    log("════ PRECONDITION: CreateKB kb1 ════")
    kb1_idem = str(uuid.uuid4())
    idem_keys.append(kb1_idem)
    status, kb1 = rest_request(
        "POST", "/knowledge-bases", body={
            "idempotency_key": kb1_idem,
            "name": f"kbrebuild-kb1-{E2E_TAG}",
            "description": "E2E issue-024 B6 RebuildKB 测试宿主 KB",
        }, expected_status=201, label="CreateKB kb1",
        desc="前置: 创建知识库 kb1（重建接口的目标 KB，含真实向量库）")
    kb1_id = kb1.get("id", "") if isinstance(kb1, dict) else ""
    check("precondition CreateKB kb1 -> 201", status == 201, f"status={status}")
    if not kb1_id:
        return 1
    log(f"  kb1_id={kb1_id}")

    # ══ Precondition: seed documents ══
    log("════ PRECONDITION: seed documents (failed / ready / pending) ════")
    doc_ids: list[str] = []
    try:
        docF = _run_async(_insert_doc(kb1_id, f"failed-{E2E_TAG}.md",
                                      "failed", "parse error: bad pdf"))
        docR = _run_async(_insert_doc(kb1_id, f"ready-{E2E_TAG}.md",
                                      "ready", None))
        docP = _run_async(_insert_doc(kb1_id, f"pending-{E2E_TAG}.md",
                                       "pending", None))
        doc_ids = [docF, docR, docP]
    except Exception as e:
        log(f"FATAL: seed failed: {e!r}")
        return 1
    log(f"  seeded: docF={docF} docR={docR} docP={docP}")
    # rebuild snapshot scope: ready+failed only; pending doc must stay
    # untouched (it is mid-pipeline; re-entering would race the consumer).

    # Mode A precondition: replace the seeded failed doc with a REAL
    # uploaded doc (the rebuild consumer re-parses via rag-engine against
    # real MinIO objects; seeded rows would 404-fail).
    doc_real = ""
    if MODE_A:
        log("════ PRECONDITION (Mode A): upload real document ════")
        up_idem = str(uuid.uuid4())
        idem_keys.append(up_idem)
        content = _doc_md_bytes()
        status, up = rest_request(
            "POST", f"/knowledge-bases/{kb1_id}/documents", body={
                "idempotency_key": up_idem,
                "file_name": f"kbrebuild-doc-{E2E_TAG}.md",
                "file_type": "md",
                "file_size_bytes": len(content),
            }, expected_status=200, label="GetUploadURL",
            desc="前置(Mode A): 获取文档上传预签名 URL")
        doc_real = up.get("doc_id", "") if isinstance(up, dict) else ""
        upload_url = up.get("upload_url", "") if isinstance(up, dict) else ""
        storage_path = up.get("storage_path", "") if isinstance(up, dict) else ""
        if doc_real and upload_url:
            doc_ids.append(doc_real)
            put_status = http_put_bytes(upload_url, content, "PUT object to MinIO")
            check("precondition presigned PUT -> 200", put_status == 200,
                  f"status={put_status}")
            notify_idem = str(uuid.uuid4())
            idem_keys.append(notify_idem)
            # guard NOT armed on the real upload path (kbnew precedent):
            # the local parse consumer also reads outbox_events directly.
            status, task = rest_request(
                "POST", f"/knowledge-bases/{kb1_id}/documents/{doc_real}/notify-uploaded",
                body={"doc_id": doc_real, "storage_path": storage_path},
                expected_status=202, label="NotifyUploaded",
                desc="前置(Mode A): 通知文档已上传（触发本地异步解析）")
            doc_state = wait_doc_ready(doc_real, timeout=300)
            if doc_state.get("parse_status") != "ready":
                log(f"  parse NOT ready (state={doc_state}) — one retry with fresh key")
                _run_async(_set_doc_status(doc_real, "pending", None))
                retry_idem = str(uuid.uuid4())
                idem_keys.append(retry_idem)
                status, task = rest_request(
                    "POST", f"/knowledge-bases/{kb1_id}/documents/{doc_real}/notify-uploaded",
                    body={"doc_id": doc_real, "storage_path": storage_path},
                    expected_status=202, label="NotifyUploaded-retry",
                    desc="前置重试(Mode A): 重新通知上传（服务端 outbox dispatcher 竞态兜底）")
                doc_state = wait_doc_ready(doc_real, timeout=300)
            check("precondition real doc parsed -> ready",
                  doc_state.get("parse_status") == "ready", f"state={doc_state}")
        else:
            log(f"  WARNING: upload URL not obtained (status={status}) — "
                "Mode A rebuild target falls back to seeded rows")

    # ══ T2: uuid validation negatives (gateway-side, pre-gRPC) ══
    log("════ T2: idempotency_key validation negatives ════")
    bad_key = f"not-a-uuid-{E2E_TAG}"

    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": bad_key},
        expected_status=400, label="T2a rebuild non-uuid key",
        desc="负例: 重建知识库使用非法 uuid 幂等键 → 400（网关层校验，先于 gRPC）")
    check("T2a non-uuid key -> 400 + 'must be a uuid'",
          status == 400 and "must be a uuid" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": ""},
        expected_status=400, label="T2b rebuild empty key",
        desc="负例: 重建知识库空幂等键 → 400（网关层校验）")
    check("T2b empty key -> 400 + 'required'",
          status == 400 and "required" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # invalid JSON body -> 400 (gateway BindJSON)
    url = REST_BASE + f"/knowledge-bases/{kb1_id}/rebuild"
    req = urllib.request.Request(url, data=b"{invalid json",
                                 method="POST", headers=REST_HDRS)
    status = -1
    try:
        resp = urllib.request.urlopen(req, timeout=30)
        status = resp.status
    except urllib.error.HTTPError as e:
        status = e.code
    except Exception as e:
        log(f"[T2c] REQUEST ERROR: {e!r}")
    check("T2c invalid JSON body -> 400", status == 400, f"status={status}")

    # ══ T3: missing KB -> 404 ══
    log("════ T3: rebuild missing KB -> 404 ════")
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{uuid.uuid4()}/rebuild",
        body={"idempotency_key": str(uuid.uuid4())},
        expected_status=404, label="T3 rebuild missing KB",
        desc="守卫: 对不存在的知识库发起重建 → 404")
    check("T3 missing KB -> 404 + 'knowledge base not found'",
          status == 404 and "knowledge base not found" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # ══ T1: RebuildKB happy path ══
    log("════ T1: POST /knowledge-bases/{kb_id}/rebuild (happy path) ════")
    key_t1 = str(uuid.uuid4())
    idem_keys.append(key_t1)
    # Pre-connect the outbox shield BEFORE the rebuild request (Mode B
    # only — server dispatcher races the shared outbox table; see
    # _OutboxGuardThread docstring).
    guard = _OutboxGuardThread(kb1_id)
    if not MODE_A:
        guard.start()
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": key_t1},
        expected_status=202, label="T1 rebuild kb1",
        desc="主路径: 对 active 知识库发起重建 → 202 + 异步任务引用"
             "（原子事务: active→rebuilding + audit + async_tasks + outbox）")
    ok_shape = (isinstance(payload, dict)
                and payload.get("task_type") == "kb.rebuild"
                and payload.get("status") == "pending"
                and payload.get("task_id"))
    check("T1a rebuild -> 202 + AsyncTaskRef shape",
          status == 202 and ok_shape, f"status={status} body={payload}")
    task_t1 = payload.get("task_id", "") if isinstance(payload, dict) else ""

    if not MODE_A:
        guard.finish()
        if guard.error:
            log(f"  T1 outbox guard ERRORED (continuing): {guard.error}")
        elif guard.result:
            log(f"  T1 outbox shield applied: {guard.result} "
                f"({guard.latency_ms:.0f}ms after arm)" if guard.latency_ms
                is not None else f"  T1 outbox shield applied: {guard.result}")
        else:
            log("  T1 outbox guard saw no event (202 likely failed) — "
                "T1d will surface the real cause")

    # T1b: KB status transition active -> rebuilding
    kb_status = _run_async(_get_kb_status(kb1_id))
    check("T1b knowledge_bases.status active->rebuilding",
          kb_status == "rebuilding", f"status={kb_status}")

    # T1c: async_tasks row — pending, payload, resource linkage
    task_row = _run_async(_fetch_task(task_t1)) if task_t1 else None
    tp = _payload_of(task_row.get("payload")) if task_row else {}
    check("T1c async_tasks row: type/status/payload/resource",
          bool(task_row)
          and task_row.get("task_type") == "kb.rebuild"
          and task_row.get("status") == "pending"
          and task_row.get("idempotency_key") == key_t1
          and task_row.get("resource_type") == "knowledge_base"
          and task_row.get("resource_id") == kb1_id
          and tp.get("kb_id") == kb1_id
          and "embedding_model" in tp
          and "chunk_size" in tp,
          f"row={task_row}")

    # T1d: outbox event — kb.rebuild on the KB aggregate, payload carries ids
    ev = _run_async(_fetch_outbox_event(kb1_id))
    ev_payload = _payload_of(ev.get("payload")) if ev else {}
    check("T1d outbox_events row: event_type/payload",
          bool(ev)
          and ev.get("event_type") == "kb.rebuild"
          and ev.get("aggregate_type") == "knowledge_bases"
          and ev.get("aggregate_id") == kb1_id
          and ev_payload.get("kb_id") == kb1_id
          and ev_payload.get("tenant_id") == TENANT_ID
          and ev_payload.get("task_id") == task_t1,
          f"event={ev}")

    # T1e: kb_audit_log row — action kb.rebuild, active -> rebuilding
    audit = _run_async(_fetch_kb_audit(kb1_id, "kb.rebuild"))
    before = _payload_of(audit.get("before_state")) if audit else {}
    after = _payload_of(audit.get("after_state")) if audit else {}
    check("T1e kb_audit_log row: action/before/after",
          bool(audit)
          and audit.get("action") == "kb.rebuild"
          and before.get("status") == "active"
          and after.get("status") == "rebuilding",
          f"audit={audit}")

    # T1f: docs NOT reset at request time (rebuild consumer resets per-doc
    # lazily; the request itself must not touch documents)
    docF_state = _run_async(_fetch_doc_state(docF))
    check("T1f doc rows untouched at request time",
          bool(docF_state) and docF_state.get("parse_status") == "failed",
          f"docF={docF_state}")

    # ══ Mode A: full-chain consumption ══
    if MODE_A:
        log("════ T9 (Mode A): rebuild consumer full chain ════")
        row = wait_task_terminal(task_t1, timeout=600)
        kb_status = _run_async(_get_kb_status(kb1_id))
        check("T9a async_tasks terminal -> completed",
              bool(row) and row.get("status") == "completed",
              f"row={row}")
        check("T9b KB back to active after rebuild",
              kb_status == "active", f"status={kb_status}")
        rp = _payload_of(row.get("result")) if row else {}
        log(f"  rebuild result={rp}")
        # ready doc (real upload) must have been re-parsed; failed seeded
        # doc re-parses against MinIO 404 (no object) -> stays failed but
        # got reset (attempted); pending doc untouched.
        if doc_real:
            doc_state = _run_async(_fetch_doc_state(doc_real))
            check("T9c real doc re-parsed -> ready",
                  bool(doc_state) and doc_state.get("parse_status") == "ready",
                  f"state={doc_state}")
        docP_state = _run_async(_fetch_doc_state(docP))
        check("T9d pending doc untouched by rebuild",
              bool(docP_state) and docP_state.get("parse_status") == "pending",
              f"state={docP_state}")

    # ══ T4: KB already rebuilding -> 409 ══
    # Mode B: natural state after T1 (no consumer to finish it).
    # Mode A: rebuild completed and the KB is active again — force the
    # rebuilding state to exercise the guard, then restore.
    log("════ T4: rebuild on rebuilding KB -> 409 ════")
    if MODE_A or kb_status != "rebuilding":
        _run_async(_set_kb_status(kb1_id, "rebuilding"))
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": str(uuid.uuid4())},
        expected_status=409, label="T4 rebuild rebuilding KB",
        desc="守卫: 对 rebuilding 状态的知识库再次发起重建 → 409（互斥）")
    check("T4 rebuilding KB -> 409 + 'rebuilding or not active'",
          status == 409
          and "knowledge base is rebuilding or not active" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")
    _run_async(_set_kb_status(kb1_id, "active"))

    # ══ T5: idempotent replay — same key re-posted -> same task_id ══
    log("════ T5: idempotent replay (same uuid key) ════")
    n_tasks_before = _run_async(_count_tasks_by_key(key_t1))
    n_events_before = _run_async(_count_outbox_events(kb1_id, "kb.rebuild"))
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": key_t1},
        expected_status=202, label="T5 rebuild replay same key",
        desc="幂等回放: 同一 uuid 幂等键二次提交 → 202 且返回同一 task_id，"
             "不新增任务行/事件")
    check("T5a replay same key -> 202 + same task_id",
          status == 202 and isinstance(payload, dict)
          and payload.get("task_id") == task_t1,
          f"status={status} task_id={payload.get('task_id') if isinstance(payload, dict) else ''}")
    n_tasks_after = _run_async(_count_tasks_by_key(key_t1))
    n_events_after = _run_async(_count_outbox_events(kb1_id, "kb.rebuild"))
    check("T5b replay: no new async_tasks row / no new outbox event",
          n_tasks_after == n_tasks_before == 1
          and n_events_after == n_events_before,
          f"tasks {n_tasks_before}->{n_tasks_after}, "
          f"events {n_events_before}->{n_events_after}")

    # ══ T6: poison-key path (failed task on the same key) ══
    log("════ T6: failed-task poison key -> 409, tx rolled back ════")
    key_t6 = str(uuid.uuid4())
    idem_keys.append(key_t6)
    poison_task = _run_async(_seed_task(
        key_t6, "kb.rebuild", "failed", resource_id=kb1_id))
    n_events_before_t6 = _run_async(_count_outbox_events(kb1_id, "kb.rebuild"))
    kb_status_before_t6 = _run_async(_get_kb_status(kb1_id))
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": key_t6},
        expected_status=409, label="T6 rebuild poison key",
        desc="毒键: 同 uuid 键的 kb.rebuild 任务已 failed → 409 拒绝重放"
             "（SPEC §5.4），且外层事务回滚（KB 留在 active）")
    check("T6a poison key -> 409 + 'already failed'",
          status == 409
          and "task already failed with this idempotency_key" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")
    # the abort() rolls back the WHOLE outer transaction including the
    # rebuilding transition — the KB must NOT have flipped to rebuilding.
    kb_status_after_t6 = _run_async(_get_kb_status(kb1_id))
    check("T6b rollback: KB stays at pre-request status",
          kb_status_after_t6 == kb_status_before_t6,
          f"{kb_status_before_t6} -> {kb_status_after_t6}")
    n_events_after_t6 = _run_async(_count_outbox_events(kb1_id, "kb.rebuild"))
    check("T6c rollback: no new outbox event",
          n_events_after_t6 == n_events_before_t6,
          f"events {n_events_before_t6}->{n_events_after_t6}")
    prow = _run_async(_fetch_task(poison_task))
    check("T6d poison task row untouched (still failed)",
          bool(prow) and prow.get("status") == "failed"
          and prow.get("task_type") == "kb.rebuild",
          f"row={prow}")

    # ══ T7: cross-tenant negative ══
    log("════ T7: cross-tenant rebuild -> 404 ════")
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": str(uuid.uuid4())},
        expected_status=404, label="T7 rebuild cross-tenant",
        headers=OTHER_HDRS,
        desc="租户隔离: 其他租户对 kb1 发起重建 → 404（RLS 隔离）")
    check("T7 cross-tenant rebuild -> 404",
          status == 404, f"status={status} msg={_msg_of(payload)!r}")

    # ══ T8: task_type-filtered replay (foreign kb.parse row) ══
    log("════ T8: task_type filter — kb.parse row with same key must NOT "
        "be replayed by rebuild ════")
    key_t8 = str(uuid.uuid4())
    idem_keys.append(key_t8)
    foreign_task = _run_async(_seed_task(key_t8, "kb.parse", "pending"))
    status, payload = rest_request(
        "POST", f"/knowledge-bases/{kb1_id}/rebuild",
        body={"idempotency_key": key_t8},
        expected_status=409, label="T8 rebuild key owned by kb.parse task",
        desc="跨类型幂等: 同 uuid 键已被 kb.parse 任务占用 → rebuild 不回放"
             "他类型任务（UNIQUE 冲突 → 409 毒键路径或 500）")
    check("T8a rebuild does NOT replay foreign-type task",
          status in (409, 500) and (
              isinstance(payload, dict)
              and payload.get("task_id") != foreign_task),
          f"status={status} foreign={foreign_task} body={payload}")
    frow = _run_async(_fetch_task(foreign_task))
    check("T8b foreign kb.parse task untouched",
          bool(frow) and frow.get("status") == "pending"
          and frow.get("task_type") == "kb.parse",
          f"row={frow}")

    # ══ cleanup ══
    log("════ CLEANUP ════")
    try:
        # Mode B: T1 left the KB in 'rebuilding' — reset before DeleteKB.
        _run_async(_set_kb_status(kb1_id, "active"))
        _run_async(_purge_seeded_rows([kb1_id], doc_ids))
        _run_async(_purge_task_and_outbox_rows(idem_keys, [kb1_id]))
        check("cleanup: seeded rows purged", True)
    except Exception as e:
        check("cleanup: seeded rows purged", False, repr(e))
    status, _ = rest_request(
        "DELETE", f"/knowledge-bases/{kb1_id}", expected_status=204,
        label="cleanup DeleteKB kb1",
        desc="清理: 软删知识库 kb1（行保留审计）")
    check("cleanup DeleteKB kb1 -> 204", status == 204, f"status={status}")

    return 0


# ── main ──────────────────────────────────────────────────────────────────────
def main():
    _open_log()
    log("════════════════════════════════════════════════════════════════")
    log(f"  E2E issue-024 (B6) RebuildKB — tag={E2E_TAG}")
    log(f"  mode={'A (full chain, consumer ON)' if MODE_A else 'B (contract + DB artifacts)'}")
    log(f"  tenant={TENANT_ID}")
    log(f"  log file: {E2E_LOG}")
    log("════════════════════════════════════════════════════════════════")

    if not GATEWAY_EXE.exists():
        log(f"FATAL: gateway exe not found at {GATEWAY_EXE} — run go build first")
        return 1

    log("── Killing stale processes on ports 8080/8001/8002/50052/50053 ──")
    _kill_stale_processes()

    # Mode A hygiene: purge leftover e2e durables whose stale filter_subject
    # would silently capture the new subscription (nats-py drift TODO).
    if MODE_A:
        _nats_purge_e2e_durables("pre-run")

    try:
        # 1. start gateway
        log("── Starting ani-gateway (:8080) ──")
        start_gateway()
        if not _wait_http("http://localhost:8080/api/v1/svc/knowledge-bases?limit=1", 60):
            log("FATAL: gateway not ready")
            return 1

        # dev-mode sanity: KB router mounted (503 = kb-service not yet up)
        status, payload = rest_request(
            "GET", "/knowledge-bases?limit=1", label="dev-mode sanity check",
            desc="启动自检: 验证网关 KB 路由已挂载（kb-service 未起时 503 "
                 "也证明路由存在）")
        check("dev sanity: KB route mounted on gateway",
              status in (200, 404, 500, 503) and status != -1, f"status={status}")
        if status == -1:
            return 1

        # 2. Mode A: start rag-engine first (kb-service consumers need it)
        if MODE_A:
            log("── Starting rag-engine (:8001 / gRPC :50052) ──")
            start_rag_engine()
            if not _wait_http("http://localhost:8001/health", 90):
                if not _wait_http("http://localhost:8001/", 15):
                    log("WARNING: rag-engine health check failed — continuing")

        # 3. start kb-service (FastAPI :8002 / gRPC :50053)
        consumer_state = ("parse=ON rebuild=ON" if MODE_A
                          else "parse=OFF rebuild=OFF")
        log(f"── Starting kb-service (:8002 / gRPC :50053, {consumer_state}) ──")
        start_kb_service()
        if not _wait_http("http://localhost:8002/health", 90):
            if not _wait_http("http://localhost:8002/", 15):
                log("WARNING: kb-service health check failed — continuing "
                    "(gRPC may still be up)")

        # wait for kb-service gRPC port to accept connections
        import socket
        ok = False
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                with socket.create_connection(("localhost", 50053), timeout=2):
                    ok = True
                    break
            except Exception:
                time.sleep(1)
        log(f"  kb-service gRPC :50053 {'ready' if ok else 'NOT reachable'}")
        if not ok:
            log("FATAL: kb-service gRPC not reachable on :50053")
            return 1

        # warm up the gateway→kb-service gRPC channel (poll until non-503)
        deadline = time.time() + 30
        warmed = False
        while time.time() < deadline:
            status, _ = rest_request(
                "GET", "/knowledge-bases?limit=1", label="grpc-channel warmup",
                desc="通道预热: 轮询直到网关→kb-service gRPC 通道退出退避"
                     "（非 503 即就绪）", timeout=10)
            if status != 503:
                warmed = True
                break
            time.sleep(2)
        if not warmed:
            log("FATAL: gateway→kb-service gRPC channel still 503 after 30s")
            return 1
        log("  gateway→kb-service gRPC channel warm")

        # 4. run E2E
        rc = run_e2e()

        # 5. summary
        n_pass = sum(1 for v in RESULTS.values() if v == "pass")
        n_fail = sum(1 for v in RESULTS.values() if v == "fail")
        log("════════════════════════════════════ RESULT SUMMARY ════════════════════════")
        log(f"  PASS: {n_pass}   FAIL: {n_fail}")
        log(f"  total checks: {len(RESULTS)}")
        if n_fail:
            log("  FAILED checks:")
            for k, v in RESULTS.items():
                if v == "fail":
                    log(f"    ✗ {k}")
        log(f"  full log: {E2E_LOG}")
        log("════════════════════════════════════════════════════════════════")
        return rc if rc else (0 if n_fail == 0 else 2)
    finally:
        _kill_all()
        # post-run durable cleanup: a hard exit before this would still leak
        # them, so the pre-run purge on the NEXT run is the real backstop.
        if MODE_A:
            _nats_purge_e2e_durables("post-run")
        log("── All services stopped ──")


if __name__ == "__main__":
    sys.exit(main())
