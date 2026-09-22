"""E2E test for B7 issue #23 — PUT /knowledge-bases/{kb_id}/config,
exercising the FULL local chain:

  REST client → ani-gateway (:8080, repo/bin/ani-gateway.exe)
             → kb-service gRPC (:50053, local main.py)
             → PostgreSQL (server NodePort 30945, real RLS)
             → NATS (server NodePort 31062, outbox events)

The endpoint under test (kb-p1-plan §5 B7 #23):
  PUT  /knowledge-bases/{kb_id}/config  (updateKnowledgeBaseConfig)

Test groups (mirrors test_update_kb_config.py coverage, through the real
gateway→gRPC→DB chain):
  P0  Non-embedding patch happy path: top_k + score_threshold +
      retrieval_mode + ocr_enabled(true→false explicit) → 200, config
      echoes the new values, rebuild_task ABSENT (omitempty; only
      embedding_model/chunk_size pair a rebuild), DB row updated,
      kb.config.update audit before/after, async_tasks replay record
      (task_type kb.config.update, completed), NO kb.rebuild task/event.
  P1  Tri-state semantics: absent fields keep current values (only top_k
      carried; embedding_model/chunk_size/ocr_enabled/score_threshold/
      retrieval_mode unchanged in DB).
  P2  embedding_model change pairs a full-KB rebuild in the SAME
      transaction: 200 + rebuild_task{task_id, kb.rebuild, pending}; DB:
      status active→rebuilding, kb.rebuild audit, kb.rebuild task with
      derived key "rebuild:<uuid>", outbox event with task_id; the
      config row carries the NEW embedding_model.
  P3  chunk_size change also pairs a rebuild (same shape as P2).
  P4  Idempotent replay: re-PUT the P2 request (same key, same body) →
      200, same rebuild task_id, no new rows (task count / event count
      unchanged) — proves the recorded result replay path.
  P5  Guards: missing KB → 404; cross-tenant → 404 (RLS); rebuilding →
      409 "knowledge base is rebuilding" (with config change rolled
      back); no-effective-change → 400 "no effective change".
  P6  Value-domain negatives (pre-gRPC-pool, INVALID_ARGUMENT): bad
      chunk_size / top_k / score_threshold / retrieval_mode / empty
      embedding_model → 400 with the range message.
  P7  Poison-key path: a prior FAILED kb.config.update task on the same
      key... NOTE — unlike RebuildKB, a failed same-type config task
      with NULL result self-heals (SAVEPOINT reuse) rather than
      rejecting; the true poison test here is the PAIRED REBUILD key
      ("rebuild:<uuid>") colliding with a FAILED kb.rebuild task → 409,
      whole outer tx rolled back (config NOT changed, KB stays active).
  P8  Gateway-side validation: missing/empty idempotency_key → 400
      "required"; non-uuid key → 400 "must be a uuid".

Outbox shield (_OutboxGuardThread, kbrebuild precedent): the
SERVER-deployed kb-service sharing this dev DB polls outbox_events every
1s; the guard marks our kb.rebuild events published within ~50ms of
commit so no stale server consumer rebuilds the KB against server-side
stores. The local rebuild consumer stays OFF (Mode B — contract + DB
artifacts); we validate the event's existence, not its consumption.

Services (local only, NEVER uploaded to the server):
  - ani-gateway (Go, :8080)      repo/bin/ani-gateway.exe (B7 rebuild)
  - kb-service  (Python, :8002 / gRPC :50053), consumers OFF

Infrastructure (server-deployed NodePorts on 10.10.1.66):
  PostgreSQL :30945 / NATS :31062

Usage:
  python tests/e2e/test_kb_update_config_e2e.py
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
E2E_LOG = KB_SVC / "tests" / "e2e" / "e2e_kb_update_config_result.log"

sys.path.insert(0, str(KB_SVC))

# ── server component endpoints (NodePorts on 10.10.1.66) ─────────────────────
SRV = "10.10.1.66"
PG_URL = f"postgres://ani_app_user:ani_dev_password@{SRV}:30945/ani?sslmode=disable"
NATS_URL = f"nats://{SRV}:31062"
REDIS_URL = f"redis://:ani_dev_password@{SRV}:30453/0"

# unique NATS subjects per run (server consumers listen on the defaults)
E2E_TAG = f"kbupcfg-{int(time.time())}"
LOCAL_PARSE_SUBJECT = f"ani.tasks.kb.parse.{E2E_TAG}"
LOCAL_PARSE_SUBJECT_V2 = f"ani.tasks.kb.parse.{E2E_TAG}.v2"
LOCAL_REBUILD_SUBJECT = f"ani.tasks.kb.rebuild.{E2E_TAG}"

TENANT_ID = "00000000-0000-0000-0000-000000000002"
OTHER_TENANT_ID = "00000000-0000-0000-0000-000000000003"

REST_BASE = "http://localhost:8080/api/v1/svc"
REST_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": TENANT_ID}
OTHER_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": OTHER_TENANT_ID}

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
    log_file = open(E2E_LOG.parent / f"{name}_kbupcfg.stdout.log", "w",
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
    for n in ("ani-gateway", "kb-service", "rag-engine"):
        try:
            subprocess.run(["taskkill", "/F", "/IM", f"{n}.exe"],
                           capture_output=True, timeout=10)
        except Exception:
            pass
    for port in (8002, 50053, 8080):
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
        "OBJECT_STORE_PROVIDER": "",
        "VECTOR_STORE_PROVIDER": "",
        "NATS_URL": NATS_URL,
        "STORAGE_PROVIDER": "",
        "K8S_CLUSTER_PROVIDER_MODE": "",
        "GPU_INVENTORY_PROVIDER": "",
        "NETWORK_PROVIDER": "",
        "REGISTRY_PROVIDER_MODE": "",
        "INSTANCE_OBSERVABILITY_PROVIDER": "",
        "KB_SERVICE_GRPC_ADDR": "localhost:50053",
        "KB_SERVICE_GRPC_CALL_TIMEOUT": "30s",
    })
    return env


def _kb_service_env():
    env = os.environ.copy()
    env.update({
        "DATABASE_URL": PG_URL,
        "NATS_URL": NATS_URL,
        # unique per-run subjects so the SERVER consumers (listening on
        # the default subjects) never receive our outbox events even if
        # the server dispatcher wins the race (kbrebuild precedent).
        "NATS_PARSE_SUBJECT": LOCAL_PARSE_SUBJECT,
        "NATS_PARSE_SUBJECT_V2": LOCAL_PARSE_SUBJECT_V2,
        "NATS_REBUILD_SUBJECT": LOCAL_REBUILD_SUBJECT,
        "ANI_GATEWAY_INTERNAL_URL": "http://localhost:8080",
        "RAG_ENGINE_GRPC_ADDR": "localhost:50052",
        "GRPC_PORT": "50053",
        "REDIS_URL": REDIS_URL,
        # Mode B: contract + DB artifacts only — both local consumers OFF
        "KB_PARSE_CONSUMER_ENABLED": "false",
        "KB_REBUILD_CONSUMER_ENABLED": "false",
        "ANI_DEV_TENANT_ID": TENANT_ID,
    })
    return env


def start_gateway():
    _start([str(GATEWAY_EXE)], _gateway_env(),
           REPO / "services" / "ani-gateway", "gateway")


def start_kb_service():
    _start([sys.executable, "main.py"], _kb_service_env(), KB_SVC, "kb-service")


# ── REST helpers ─────────────────────────────────────────────────────────────
def rest_request(method: str, path: str, body: dict | None = None,
                 expected_status: int | None = None, label: str = "",
                 timeout: float = 60.0, headers: dict | None = None,
                 desc: str = ""):
    """Send a REST request through the gateway; log the interface purpose
    (desc), input and output. Returns (status_code, parsed_body)."""
    url = REST_BASE + path
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
        log_json(f"[{label}] INPUT BODY", body)
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
    log(f"[{label}] OUTPUT: HTTP {status}"
        + (f" (期望 {expected_status})" if expected_status is not None else ""))
    log_json(f"[{label}] OUTPUT BODY", payload)
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
    """Ensure tenant rows for TENANT_ID / OTHER_TENANT_ID exist (FK source)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        for tid, name in ((TENANT_ID, "kbupcfg-dev-tenant"),
                          (OTHER_TENANT_ID, "kbupcfg-other-tenant")):
            await conn.execute(
                "INSERT INTO tenants (id, name, display_name, plan_id) "
                "VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING",
                uuid.UUID(tid), name, name,
                uuid.UUID("00000000-0000-0000-0000-000000000001"))
        log(f"  tenants ensured: {TENANT_ID} / {OTHER_TENANT_ID}")
    finally:
        await conn.close()


async def _get_kb_row(kb_id: str) -> dict | None:
    """Read the full config-relevant KB row (RLS-scoped)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT id::text, name, embedding_model, chunk_size,
                       ocr_enabled, top_k, score_threshold::float8,
                       retrieval_mode, status
                  FROM knowledge_bases WHERE id = $1
                """,
                uuid.UUID(kb_id))
            return dict(row) if row else None
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


async def _seed_task(idem_key: str, task_type: str, status: str,
                    resource_id: str | None = None,
                    payload: dict | None = None) -> str:
    """Insert an async_tasks row with a GIVEN task_type/status under the
    same tenant (poison-key probes). Returns the id."""
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


async def _fetch_task_by_key(idem_key: str, task_type: str) -> dict | None:
    """Read the async_tasks row for (tenant, key, task_type)."""
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
                       result
                  FROM async_tasks
                 WHERE tenant_id = $1 AND idempotency_key = $2
                   AND task_type = $3
                """,
                uuid.UUID(TENANT_ID), idem_key, task_type)
            return dict(row) if row else None
    finally:
        await conn.close()


async def _count_tasks_by_key(idem_key: str) -> int:
    """Count async_tasks rows for a config key AND its paired rebuild
    derived key ("rebuild:<uuid>") — one logical request may leave two
    rows (kb.config.update + kb.rebuild)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            n = await conn.fetchval(
                "SELECT COUNT(*) FROM async_tasks "
                "WHERE tenant_id=$1 AND idempotency_key IN ($2, $3)",
                uuid.UUID(TENANT_ID), idem_key, f"rebuild:{idem_key}")
            return int(n)
    finally:
        await conn.close()


async def _fetch_kb_audit(kb_id: str, action: str) -> dict | None:
    """Latest kb_audit_log row for (kb_id, action)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT action, actor_user_id::text, before_state,
                       after_state, error_code, error_msg
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
    """Latest kb.rebuild outbox event for a KB (RLS-scoped)."""
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


async def _count_outbox_events(kb_id: str) -> int:
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            n = await conn.fetchval(
                "SELECT COUNT(*) FROM outbox_events "
                "WHERE aggregate_id=$1 AND event_type='kb.rebuild'",
                uuid.UUID(kb_id))
            return int(n)
    finally:
        await conn.close()


def _payload_of(raw: Any) -> dict:
    if isinstance(raw, str):
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {}
    return raw or {}


# ── outbox shield (kbrebuild precedent, Mode B) ──────────────────────────────
class _OutboxGuardThread:
    """Pre-connected watchdog that shields kb.rebuild outbox events from
    the SERVER-deployed kb-service sharing the dev DB (it polls
    outbox_events every 1s and would publish to the SERVER subject).

    Pre-connecting shrinks the post-commit race window to ~50ms (see the
    kbrebuild E2E for the full rationale; identical threat model).
    """

    def __init__(self, kb_id: str):
        self._kb_id = kb_id
        self._stop = threading.Event()
        self._ready = threading.Event()
        self._done = threading.Event()
        self._thread: threading.Thread | None = None
        self.result: str = ""
        self.latency_ms: float | None = None
        self.error: str = ""

    def _run(self):
        import asyncio
        import asyncpg

        async def guard_loop():
            conn = await asyncpg.connect(PG_URL)
            try:
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
                    await asyncio.sleep(0.01)
            finally:
                self._done.set()
                await conn.close()

        try:
            asyncio.run(guard_loop())
        except Exception as e:  # noqa: BLE001
            self.error = repr(e)
            self._ready.set()
            self._done.set()

    def start(self, connect_timeout: float = 5.0) -> bool:
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


# ── cleanup ──────────────────────────────────────────────────────────────────
async def _purge_kbs_and_docs(kb_ids: list[str]):
    """Physically purge seeded KBs + docs (soft-delete rows would linger
    in the shared dev DB; e2e data must not)."""
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
                await conn.execute(
                    "DELETE FROM knowledge_bases WHERE id=$1", uuid.UUID(kb_id))
    finally:
        await conn.close()


async def _neutralize_task_and_outbox_rows(idem_keys: list[str],
                                           kb_ids: list[str]):
    """Neutralize async_tasks / outbox_events rows this run created.

    ani_app has no DELETE on these audit tables — mark outbox events
    published (the server dispatcher never sees them) and attempt the
    async_tasks DELETE (graceful degrade on denial)."""
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
                 WHERE tenant_id=$1
                   AND (aggregate_id = ANY($2::uuid[])
                        OR payload->>'task_id' IN
                            (SELECT id::text FROM async_tasks
                              WHERE tenant_id=$1
                                AND idempotency_key = ANY($3::text[])))
                   AND published = FALSE
                """,
                uuid.UUID(TENANT_ID),
                [uuid.UUID(k) for k in kb_ids],
                list(idem_keys))
            log(f"  neutralize outbox_events: {res}")
        try:
            async with conn.transaction():
                await conn.execute(
                    f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
                res = await conn.execute(
                    """
                    DELETE FROM async_tasks
                     WHERE tenant_id=$1
                       AND (idempotency_key = ANY($2::text[])
                            OR idempotency_key = ANY($3::text[]))
                    """,
                    uuid.UUID(TENANT_ID),
                    list(idem_keys),
                    [f"rebuild:{k}" for k in idem_keys])
                log(f"  purge async_tasks (incl. rebuild:* keys): {res}")
        except Exception as e:
            log(f"  async_tasks DELETE denied (audit table, expected on "
                f"shared dev DB): {e!r}")
    finally:
        await conn.close()


# ── run_e2e ───────────────────────────────────────────────────────────────────
def run_e2e():
    idem_keys: list[str] = []

    # ══ Precondition: CreateKB kb1 with known config ══
    log("════ PRECONDITION: CreateKB kb1 ════")
    kb1_idem = str(uuid.uuid4())
    idem_keys.append(kb1_idem)
    status, kb1 = rest_request(
        "POST", "/knowledge-bases", body={
            "idempotency_key": kb1_idem,
            "name": f"{E2E_TAG}-kb1",
            "description": "B7 #23 PUT config e2e 测试宿主 KB",
            # seed distinct values so every later patch is a real change
            "embedding_model": "bge-m3",
            "chunk_size": 768,
            "top_k": 8,
            "score_threshold": 0.35,
            "retrieval_mode": "keyword",
        }, expected_status=201, label="CreateKB kb1",
        desc="前置: 创建知识库 kb1（以已知非默认配置值作为 PUT config 的基线）")
    kb1_id = kb1.get("id", "") if isinstance(kb1, dict) else ""
    check("precondition CreateKB kb1 -> 201", status == 201, f"status={status}")
    if not kb1_id:
        return 1
    log(f"  kb1_id={kb1_id}")

    # baseline DB row (ocr_enabled column default false via migration 007)
    row0 = _run_async(_get_kb_row(kb1_id)) or {}
    check("precondition baseline DB row",
          row0.get("embedding_model") == "bge-m3"
          and row0.get("chunk_size") == 768
          and row0.get("top_k") == 8
          and abs(float(row0.get("score_threshold") or -1) - 0.35) < 1e-6
          and row0.get("retrieval_mode") == "keyword"
          and row0.get("ocr_enabled") is False
          and row0.get("status") == "active",
          f"row={row0}")

    # ══ P6 first (pure negatives, no side effects): value domain ══
    log("════ P6: value-domain negatives → 400 INVALID_ARGUMENT ════")
    p6_cases = [
        ("chunk_size=0", {"chunk_size": 0}, "chunk_size must be in [1, 8192]"),
        ("chunk_size=8193", {"chunk_size": 8193}, "chunk_size must be in [1, 8192]"),
        ("top_k=0", {"top_k": 0}, "top_k must be in [1, 20]"),
        ("top_k=21", {"top_k": 21}, "top_k must be in [1, 20]"),
        ("score_threshold=1.5", {"score_threshold": 1.5}, "score_threshold must be in [0, 1]"),
        ("retrieval_mode=bogus", {"retrieval_mode": "bogus"}, "retrieval_mode must be one of"),
        ("embedding_model empty", {"embedding_model": ""}, "embedding_model must not be empty"),
    ]
    for name, patch, want_msg in p6_cases:
        status, payload = rest_request(
            "PUT", f"/knowledge-bases/{kb1_id}/config",
            body={"idempotency_key": str(uuid.uuid4()), **patch},
            expected_status=400, label=f"P6 {name}",
            desc=f"值域负例: {name} → 400（先于池获取，无 DB 副作用）")
        check(f"P6 {name} -> 400 + range message",
              status == 400 and want_msg in _msg_of(payload),
              f"status={status} msg={_msg_of(payload)!r}")

    # ══ P8: gateway-side idempotency_key validation ══
    log("════ P8: gateway idempotency_key validation negatives ════")
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": "", "top_k": 9},
        expected_status=400, label="P8a empty key",
        desc="负例: 空幂等键 → 400（网关层校验，先于 gRPC）")
    check("P8a empty key -> 400 + 'required'",
          status == 400 and "required" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": "not-a-uuid", "top_k": 9},
        expected_status=400, label="P8b non-uuid key",
        desc="负例: 非 uuid 幂等键 → 400（网关层校验）")
    check("P8b non-uuid key -> 400 + 'must be a uuid'",
          status == 400 and "must be a uuid" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": str(uuid.uuid4())},
        expected_status=400, label="P8c body without any field",
        desc="负例: 只带幂等键、无任何配置字段 → 400（kb-service 空变更拒绝）")
    check("P8c empty patch -> 400 + 'no effective change'",
          status == 400 and "no effective change" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # invalid JSON body -> 400 (gateway BindJSON)
    url = REST_BASE + f"/knowledge-bases/{kb1_id}/config"
    req = urllib.request.Request(url, data=b"{invalid json",
                                 method="PUT", headers=REST_HDRS)
    status = -1
    try:
        resp = urllib.request.urlopen(req, timeout=30)
        status = resp.status
    except urllib.error.HTTPError as e:
        status = e.code
    except Exception as e:
        log(f"[P8d] REQUEST ERROR: {e!r}")
    check("P8d invalid JSON body -> 400", status == 400, f"status={status}")

    # ══ P5a: missing KB → 404 ══
    log("════ P5a: PUT config on missing KB -> 404 ════")
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{uuid.uuid4()}/config",
        body={"idempotency_key": str(uuid.uuid4()), "top_k": 5},
        expected_status=404, label="P5a missing KB",
        desc="守卫: 对不存在的知识库改配置 → 404")
    check("P5a missing KB -> 404 + 'knowledge base not found'",
          status == 404 and "knowledge base not found" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # ══ P5b: cross-tenant → 404 (RLS) ══
    log("════ P5b: cross-tenant PUT config -> 404 ════")
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": str(uuid.uuid4()), "top_k": 5},
        expected_status=404, label="P5b cross-tenant",
        headers=OTHER_HDRS,
        desc="租户隔离: 其他租户改 kb1 配置 → 404（RLS 隔离）")
    check("P5b cross-tenant -> 404",
          status == 404, f"status={status} msg={_msg_of(payload)!r}")

    # ══ P0: non-embedding patch happy path ══
    log("════ P0: PUT /config — non-embedding patch (top_k + "
        "score_threshold + retrieval_mode + ocr_enabled) ════")
    key_p0 = str(uuid.uuid4())
    idem_keys.append(key_p0)
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": key_p0,
            "top_k": 12,
            "score_threshold": 0.55,
            "retrieval_mode": "hybrid",
            "ocr_enabled": True,   # false→true: explicit change candidate
        }, expected_status=200, label="P0 non-embedding patch",
        desc="主路径: 非向量参数补丁（top_k/score_threshold/retrieval_mode/"
             "ocr_enabled）→ 200 + 新配置回显，rebuild_task 省略（不触发重建）")
    check("P0a non-embedding patch -> 200 + new config echo",
          status == 200 and isinstance(payload, dict)
          and payload.get("top_k") == 12
          and abs(float(payload.get("score_threshold", -1)) - 0.55) < 1e-6
          and payload.get("retrieval_mode") == "hybrid"
          and payload.get("ocr_enabled") is True
          and payload.get("embedding_model") == "bge-m3"   # untouched
          and payload.get("chunk_size") == 768,            # untouched
          f"status={status} body={payload}")
    check("P0b rebuild_task ABSENT (no embedding change)",
          isinstance(payload, dict) and "rebuild_task" not in payload,
          f"keys={sorted(payload.keys()) if isinstance(payload, dict) else payload}")

    # DB truth: the row really holds the new values
    row_p0 = _run_async(_get_kb_row(kb1_id)) or {}
    check("P0c DB row holds new values (ocr/其余字段)",
          row_p0.get("top_k") == 12
          and abs(float(row_p0.get("score_threshold") or -1) - 0.55) < 1e-6
          and row_p0.get("retrieval_mode") == "hybrid"
          and row_p0.get("ocr_enabled") is True
          and row_p0.get("embedding_model") == "bge-m3"
          and row_p0.get("chunk_size") == 768
          and row_p0.get("status") == "active",
          f"row={row_p0}")

    # audit: kb.config.update with before/after states
    audit = _run_async(_fetch_kb_audit(kb1_id, "kb.config.update"))
    before = _payload_of(audit.get("before_state")) if audit else {}
    after = _payload_of(audit.get("after_state")) if audit else {}
    check("P0d kb.config.update audit before/after",
          bool(audit)
          and before.get("top_k") == 8 and before.get("retrieval_mode") == "keyword"
          and after.get("top_k") == 12 and after.get("retrieval_mode") == "hybrid"
          and after.get("ocr_enabled") is True,
          f"before={before} after={after}")

    # P0d-2: with live documents, audit doc_count must be the live count,
    # not the stale physical column (regression guard for the RETURNING
    # subquery fix — the physical doc_count column is never maintained).
    async def _seed_two_docs(kb_id: str) -> None:
        import asyncpg
        conn = await asyncpg.connect(PG_URL)
        try:
            async with conn.transaction():
                await conn.execute(
                    f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
                for n in (1, 2):
                    await conn.execute(
                        """
                        INSERT INTO kb_documents
                            (tenant_id, kb_id, file_name, file_type,
                             file_size_bytes, storage_path, checksum_sha256,
                             parse_status, error_message)
                        VALUES ($1, $2, $3, 'txt', 16, $4, $5,
                                'ready', NULL)
                        """,
                        uuid.UUID(TENANT_ID), uuid.UUID(kb_id),
                        f"{E2E_TAG}-d{n}",
                        f"{E2E_TAG}/d{n}.txt",
                        f"{E2E_TAG}-sha256-d{n}")
        finally:
            await conn.close()

    _run_async(_seed_two_docs(kb1_id))
    key_p0d2 = str(uuid.uuid4())
    idem_keys.append(key_p0d2)
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": key_p0d2, "top_k": 9},
        expected_status=200, label="P0d-2 doc_count audit",
        desc="回归防护: 有 2 条活跃文档时更新 top_k → 审计 after.doc_count "
             "与任务 result 内嵌 config.doc_count 应为活跃数 2"
             "（而非恒 0 的物理列）")
    check("P0d-2a 200 + task result doc_count == 2 (live count)",
          status == 200 and isinstance(payload, dict)
          and payload.get("top_k") == 9
          and _payload_of(_run_async(
              _fetch_task_by_key(key_p0d2, "kb.config.update")
          ).get("result")).get("config", {}).get("doc_count") == 2,
          f"status={status} body={payload}")
    audit2 = _run_async(_fetch_kb_audit(kb1_id, "kb.config.update"))
    after2 = _payload_of(audit2.get("after_state")) if audit2 else {}
    check("P0d-2b audit after doc_count == 2",
          after2.get("doc_count") == 2,
          f"after={after2}")

    # idempotency record: kb.config.update task completed with result
    trow = _run_async(_fetch_task_by_key(key_p0, "kb.config.update"))
    tres = _payload_of(trow.get("result")) if trow else {}
    check("P0e async_tasks kb.config.update completed + result",
          bool(trow) and trow.get("status") == "completed"
          and trow.get("resource_type") == "knowledge_base"
          and trow.get("resource_id") == kb1_id
          and tres.get("config", {}).get("top_k") == 12
          and tres.get("rebuild_task") is None,
          f"row={trow}")

    # NO kb.rebuild task/event was created
    n_events_p0 = _run_async(_count_outbox_events(kb1_id))
    check("P0f NO kb.rebuild task/event for non-embedding patch",
          n_events_p0 == 0, f"events={n_events_p0}")

    # ══ P1: tri-state — absent fields keep current values ══
    log("════ P1: tri-state (only top_k carried; the rest keep current) ════")
    key_p1 = str(uuid.uuid4())
    idem_keys.append(key_p1)
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": key_p1, "top_k": 5},
        expected_status=200, label="P1 tri-state single field",
        desc="三态语义: 只携带 top_k → 其余字段保持现值（区别于 COALESCE 语义）")
    row_p1 = _run_async(_get_kb_row(kb1_id)) or {}
    check("P1a absent fields keep current values",
          status == 200
          and row_p1.get("top_k") == 5
          and abs(float(row_p1.get("score_threshold") or -1) - 0.55) < 1e-6
          and row_p1.get("retrieval_mode") == "hybrid"
          and row_p1.get("ocr_enabled") is True
          and row_p1.get("embedding_model") == "bge-m3"
          and row_p1.get("chunk_size") == 768,
          f"status={status} row={row_p1}")
    check("P1b no rebuild_task (top_k only)",
          isinstance(payload, dict) and "rebuild_task" not in payload,
          f"body={payload}")

    # ══ P2: embedding_model change pairs a full-KB rebuild ══
    log("════ P2: PUT /config — embedding_model change → paired rebuild ════")
    key_p2 = str(uuid.uuid4())
    idem_keys.append(key_p2)
    guard = _OutboxGuardThread(kb1_id)
    guard.start()
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": key_p2,
            "embedding_model": "Qwen3-Embedding-0.6B",
        }, expected_status=200, label="P2 embedding_model change",
        desc="联动重建: 更换 embedding_model → 同事务触发全库重建"
             "（200 + rebuild_task + active→rebuilding + outbox）")
    guard.finish()
    if guard.error:
        log(f"  P2 outbox guard ERRORED (continuing): {guard.error}")
    elif guard.result:
        log(f"  P2 outbox shield applied: {guard.result} "
            f"({guard.latency_ms:.0f}ms after arm)")
    else:
        log("  P2 outbox guard saw no event (200 likely failed) — "
            "P2c will surface the real cause")

    check("P2a embedding change -> 200 + rebuild_task ref",
          status == 200 and isinstance(payload, dict)
          and payload.get("embedding_model") == "Qwen3-Embedding-0.6B"
          and isinstance(payload.get("rebuild_task"), dict)
          and payload["rebuild_task"].get("task_type") == "kb.rebuild"
          and payload["rebuild_task"].get("status") == "pending"
          and payload["rebuild_task"].get("task_id"),
          f"status={status} body={payload}")
    rebuild_task_id = ""
    if isinstance(payload, dict) and isinstance(payload.get("rebuild_task"), dict):
        rebuild_task_id = payload["rebuild_task"].get("task_id", "")

    # KB status active → rebuilding (same transaction)
    row_p2 = _run_async(_get_kb_row(kb1_id)) or {}
    check("P2b KB active->rebuilding + new embedding in DB",
          row_p2.get("status") == "rebuilding"
          and row_p2.get("embedding_model") == "Qwen3-Embedding-0.6B",
          f"row={row_p2}")

    # kb.rebuild async task with DERIVED key rebuild:<uuid>
    rtask = _run_async(_fetch_task_by_key(f"rebuild:{key_p2}", "kb.rebuild"))
    check("P2c kb.rebuild task with derived key rebuild:<uuid>",
          bool(rtask) and rtask.get("status") == "pending"
          and rtask.get("resource_id") == kb1_id
          and str(rtask.get("id")) == rebuild_task_id,
          f"row={rtask} want_id={rebuild_task_id}")

    # outbox event carries kb_id/tenant_id/task_id
    ev = _run_async(_fetch_outbox_event(kb1_id))
    evp = _payload_of(ev.get("payload")) if ev else {}
    check("P2d outbox kb.rebuild event payload",
          bool(ev) and ev.get("aggregate_type") == "knowledge_bases"
          and ev.get("aggregate_id") == kb1_id
          and evp.get("kb_id") == kb1_id
          and evp.get("tenant_id") == TENANT_ID
          and evp.get("task_id") == rebuild_task_id,
          f"event={ev}")

    # kb.rebuild audit + kb.config.update audit both present
    audit_rb = _run_async(_fetch_kb_audit(kb1_id, "kb.rebuild"))
    audit_cfg = _run_async(_fetch_kb_audit(kb1_id, "kb.config.update"))
    acfg_after = _payload_of(audit_cfg.get("after_state")) if audit_cfg else {}
    check("P2e dual audit (kb.rebuild + kb.config.update)",
          bool(audit_rb) and bool(audit_cfg)
          and acfg_after.get("embedding_model") == "Qwen3-Embedding-0.6B",
          f"rebuild_audit={audit_rb} cfg_after={acfg_after}")

    # config idempotency record: result carries the rebuild ref
    crow = _run_async(_fetch_task_by_key(key_p2, "kb.config.update"))
    cres = _payload_of(crow.get("result")) if crow else {}
    check("P2f config task result embeds rebuild_task",
          bool(crow) and crow.get("status") == "completed"
          and isinstance(cres.get("rebuild_task"), dict)
          and cres["rebuild_task"].get("task_id") == rebuild_task_id
          and cres.get("config", {}).get("embedding_model") == "Qwen3-Embedding-0.6B",
          f"row={crow}")

    # ══ P5c: rebuilding KB → 409 (natural state after P2) ══
    log("════ P5c: PUT config on rebuilding KB -> 409 ════")
    kb_status = (_run_async(_get_kb_row(kb1_id)) or {}).get("status")
    if kb_status != "rebuilding":
        _run_async(_set_kb_status(kb1_id, "rebuilding"))
    row_before_409 = _run_async(_get_kb_row(kb1_id)) or {}
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={"idempotency_key": str(uuid.uuid4()), "top_k": 3},
        expected_status=409, label="P5c rebuilding KB",
        desc="守卫: rebuilding 状态的知识库改配置 → 409（互斥）")
    check("P5c rebuilding KB -> 409 + 'knowledge base is rebuilding'",
          status == 409
          and "knowledge base is rebuilding" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # failure audit recorded for the 409
    audit_fail = _run_async(_fetch_kb_audit(kb1_id, "kb.config.update"))
    check("P5c-2 failure audit for the 409 recorded",
          bool(audit_fail) and audit_fail.get("error_code") is not None,
          f"audit={audit_fail}")

    # ══ P4: idempotent replay of the P2 request (same key, same body) ══
    log("════ P4: idempotent replay (same key re-PUT the P2 body) ════")
    _run_async(_set_kb_status(kb1_id, "active"))  # leave rebuilding for replay
    n_tasks_before = _run_async(_count_tasks_by_key(key_p2))
    n_events_before = _run_async(_count_outbox_events(kb1_id))
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": key_p2,
            "embedding_model": "Qwen3-Embedding-0.6B",
        }, expected_status=200, label="P4 replay same key",
        desc="幂等回放: 同一幂等键重放 P2 请求 → 200 回放首次结果"
             "（相同 rebuild task_id，不新增任何行）")
    check("P4a replay -> 200 + same rebuild task_id",
          status == 200 and isinstance(payload, dict)
          and isinstance(payload.get("rebuild_task"), dict)
          and payload["rebuild_task"].get("task_id") == rebuild_task_id
          and payload.get("embedding_model") == "Qwen3-Embedding-0.6B",
          f"status={status} body={payload}")
    n_tasks_after = _run_async(_count_tasks_by_key(key_p2))
    n_events_after = _run_async(_count_outbox_events(kb1_id))
    check("P4b replay: no new task rows / outbox events",
          n_tasks_after == n_tasks_before == 2   # config + rebuild:* rows
          and n_events_after == n_events_before,
          f"tasks {n_tasks_before}->{n_tasks_after}, "
          f"events {n_events_before}->{n_events_after}")
    # replay while rebuilding must NOT have re-triggered or 409'd: the
    # recorded result is returned BEFORE the gate check.
    row_p4 = _run_async(_get_kb_row(kb1_id)) or {}
    check("P4c replay leaves KB active (no gate re-check)",
          row_p4.get("status") == "active", f"row={row_p4}")

    # ══ P3: chunk_size change pairs a rebuild ══
    log("════ P3: PUT /config — chunk_size change → paired rebuild ════")
    key_p3 = str(uuid.uuid4())
    idem_keys.append(key_p3)
    guard3 = _OutboxGuardThread(kb1_id)
    guard3.start()
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": key_p3,
            "chunk_size": 2048,
        }, expected_status=200, label="P3 chunk_size change",
        desc="联动重建: 修改 chunk_size → 同事务触发全库重建（chunk 维度变更）")
    guard3.finish()
    if guard3.error:
        log(f"  P3 outbox guard ERRORED (continuing): {guard3.error}")
    row_p3 = _run_async(_get_kb_row(kb1_id)) or {}
    rt3 = payload.get("rebuild_task", {}) if isinstance(payload, dict) else {}
    rtask3 = _run_async(
        _fetch_task_by_key(f"rebuild:{key_p3}", "kb.rebuild"))
    check("P3a chunk_size change -> 200 + rebuild_task + DB rebuilt",
          status == 200
          and payload.get("chunk_size") == 2048
          and isinstance(rt3, dict) and rt3.get("task_id")
          and row_p3.get("status") == "rebuilding"
          and row_p3.get("chunk_size") == 2048
          and bool(rtask3) and str(rtask3.get("id")) == rt3.get("task_id"),
          f"status={status} row={row_p3} rt={rt3}")

    # ══ P7: poison key — failed kb.rebuild on derived key → 409 rollback ══
    log("════ P7: paired-rebuild poison key -> 409 + full rollback ════")
    _run_async(_set_kb_status(kb1_id, "active"))
    key_p7 = str(uuid.uuid4())
    idem_keys.append(key_p7)
    poison_task = _run_async(_seed_task(
        f"rebuild:{key_p7}", "kb.rebuild", "failed", resource_id=kb1_id))
    row_before_p7 = _run_async(_get_kb_row(kb1_id)) or {}
    n_events_before_p7 = _run_async(_count_outbox_events(kb1_id))
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": key_p7,
            "embedding_model": "poison-model-x",
        }, expected_status=409, label="P7 poison rebuild key",
        desc="毒键回滚: 配置更新联动重建时，派生键 rebuild:<uuid> 已有 failed "
             "任务 → 409 且整个外层事务回滚（配置未变、KB 留在 active）")
    check("P7a poison paired-rebuild key -> 409 + 'already failed'",
          status == 409
          and "task already failed with this idempotency_key" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")
    row_after_p7 = _run_async(_get_kb_row(kb1_id)) or {}
    check("P7b rollback: config NOT changed, KB stays active",
          row_after_p7.get("embedding_model") == "Qwen3-Embedding-0.6B"
          and row_after_p7.get("status") == "active",
          f"row={row_after_p7}")
    n_events_after_p7 = _run_async(_count_outbox_events(kb1_id))
    check("P7c rollback: no new outbox event",
          n_events_after_p7 == n_events_before_p7,
          f"events {n_events_before_p7}->{n_events_after_p7}")
    prow = _run_async(_fetch_task_by_key(f"rebuild:{key_p7}", "kb.rebuild"))
    check("P7d poison task row untouched (still failed)",
          bool(prow) and prow.get("status") == "failed",
          f"row={prow}")

    # ══ P5d: no-effective-change → 400 ══
    log("════ P5d: no-effective-change -> 400 ════")
    status, payload = rest_request(
        "PUT", f"/knowledge-bases/{kb1_id}/config",
        body={
            "idempotency_key": str(uuid.uuid4()),
            # same values as current DB row — nothing changes
            "embedding_model": "Qwen3-Embedding-0.6B",
            "chunk_size": 2048,
            "top_k": 5,
            "retrieval_mode": "hybrid",
        }, expected_status=400, label="P5d no effective change",
        desc="负例: 携带字段与现值全部相同 → 400 'no effective change'")
    check("P5d all-same-values patch -> 400 + 'no effective change'",
          status == 400 and "no effective change" in _msg_of(payload),
          f"status={status} msg={_msg_of(payload)!r}")

    # ══ GET /config cross-check: the final state through the read API ══
    log("════ FINAL: GET /config cross-check ════")
    status, payload = rest_request(
        "GET", f"/knowledge-bases/{kb1_id}/config",
        expected_status=200, label="GET config final",
        desc="交叉验证: 读取最终配置，应与 DB 行一致（P0/P1/P2/P3 的累积效果）")
    row_final = _run_async(_get_kb_row(kb1_id)) or {}
    check("GET config final == DB row",
          status == 200 and isinstance(payload, dict)
          and payload.get("embedding_model") == "Qwen3-Embedding-0.6B"
          and payload.get("chunk_size") == 2048
          and payload.get("top_k") == 5
          and payload.get("retrieval_mode") == "hybrid"
          and payload.get("ocr_enabled") is True
          and abs(float(payload.get("score_threshold", -1)) - 0.55) < 1e-6,
          f"payload={payload} row={row_final}")

    # ══ cleanup ══
    log("════ CLEANUP ════")
    try:
        _run_async(_set_kb_status(kb1_id, "active"))
        _run_async(_purge_kbs_and_docs([kb1_id]))
        _run_async(_neutralize_task_and_outbox_rows(idem_keys, [kb1_id]))
        check("cleanup: seeded rows purged", True)
    except Exception as e:
        check("cleanup: seeded rows purged", False, repr(e))

    return 0


# ── main ──────────────────────────────────────────────────────────────────────
def main():
    _open_log()
    log("════════════════════════════════════════════════════════════════")
    log(f"  B7 #23 PUT /config E2E — tag={E2E_TAG}")
    log(f"  tenant={TENANT_ID}")
    log(f"  log file: {E2E_LOG}")
    log("════════════════════════════════════════════════════════════════")

    if not GATEWAY_EXE.exists():
        log(f"FATAL: gateway exe not found at {GATEWAY_EXE} — run go build first")
        return 1

    log("── Killing stale processes on ports 8080/8002/50053 ──")
    _kill_stale_processes()

    try:
        # 0. ensure tenants (FK source for CreateKB)
        _run_async(_ensure_tenant_rows())

        # 1. start gateway
        log("── Starting ani-gateway (:8080) ──")
        start_gateway()
        if not _wait_http("http://localhost:8080/api/v1/svc/knowledge-bases?limit=1", 60):
            log("FATAL: gateway not ready")
            return 1

        # 2. start kb-service (FastAPI :8002 / gRPC :50053, consumers OFF)
        log("── Starting kb-service (:8002 / gRPC :50053, consumers OFF) ──")
        start_kb_service()
        if not _wait_http("http://localhost:8002/health", 90):
            if not _wait_http("http://localhost:8002/", 15):
                log("WARNING: kb-service health check failed — continuing "
                    "(gRPC may still be up)")

        # wait for the gRPC port
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

        # warm the gateway→kb-service gRPC channel (poll until non-503)
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

        # 3. run E2E
        rc = run_e2e()

        # 4. summary
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
        log("── All services stopped ──")


if __name__ == "__main__":
    sys.exit(main())
