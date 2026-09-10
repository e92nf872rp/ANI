"""E2E test for the B4 permissions pair (issue #19 GET + #20 PUT) —
/knowledge-bases/{kb_id}/permissions, exercising the FULL local chain:

  REST client → ani-gateway (:8080, repo/bin/ani-gateway.exe)
             → kb-service gRPC (:50053, local main.py)
             → PostgreSQL (server NodePort 30945, real RLS)

The 2 endpoints under test (kb-p1-plan §2.6):
  #20 PUT  /knowledge-bases/{kb_id}/permissions  (updateKBPermissions)
  #19 GET  /knowledge-bases/{kb_id}/permissions  (getKBPermissions)

Business flow (real KB, no document upload needed — permissions are pure
metadata on the KB row):
  1. CreateKB (exercises the existing happy path)
  2. GET permissions before any PUT → default contract values
     (public_read=false, allowed_user_ids=[], updated_at=kb.created_at) —
     NOT 404; only a missing KB yields 404
  3. PUT permissions (public_read=true, allowed_user_ids=[u1, u2]) → 200
     + KB snapshot body
  4. GET permissions → persisted values + fresh updated_at
  5. PUT again with the SAME idempotency_key → idempotent replay (200,
     same result, no duplicated async_tasks row)
  6. PUT again with a NEW idempotency_key + NEW values → real update
     (allowed_user_ids replace, public_read flip)
  7. Negative: PUT with invalid uuid in allowed_user_ids → 400
  8. Negative: PUT with missing idempotency_key → 400
  9. Negative: GET permissions on missing KB → 404
  10. Negative: GET/PUT with a kb_id belonging to ANOTHER tenant → 404
     (RLS tenant isolation through the whole chain)
  11. Cleanup: DeleteKB (soft) + direct purge of the KB row so the shared
     dev DB stays clean

Services (local only, NEVER uploaded to the server):
  - ani-gateway (Go, :8080)      repo/bin/ani-gateway.exe
  - kb-service  (Python, gRPC :50053)

Infrastructure (server-deployed NodePorts on 10.10.1.66):
  PostgreSQL :30945  (kb_permissions table applied via migration 005)
  (NATS/Redis not needed for this flow; kb-service tolerates them being
   unreachable — no parse/query in this test)

Usage:
  python tests/e2e/test_kb_permissions_e2e.py
"""
from __future__ import annotations

import io
import json
import os
import subprocess
import sys
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
E2E_LOG = KB_SVC / "tests" / "e2e" / "e2e_kb_permissions_result.log"

sys.path.insert(0, str(KB_SVC))

# ── server component endpoints (NodePorts on 10.10.1.66) ─────────────────────
SRV = "10.10.1.66"
PG_URL = f"postgres://ani_app_user:ani_dev_password@{SRV}:30945/ani?sslmode=disable"

TENANT_ID = "00000000-0000-0000-0000-000000000002"
OTHER_TENANT_ID = "00000000-0000-0000-0000-000000000003"
E2E_TAG = f"kbperm-{int(time.time())}"

REST_BASE = "http://localhost:8080/api/v1/svc"
REST_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": TENANT_ID}
OTHER_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": OTHER_TENANT_ID}

USER_1 = "11111111-1111-1111-1111-111111111111"
USER_2 = "22222222-2222-2222-2222-222222222222"
USER_3 = "33333333-3333-3333-3333-333333333333"

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
    log_file = open(E2E_LOG.parent / f"{name}_kbperm.stdout.log", "w",
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
    for n in ("ani-gateway", "kb-service", "uvicorn"):
        try:
            subprocess.run(
                ["taskkill", "/F", "/IM", f"{n}.exe"],
                capture_output=True, timeout=10,
            )
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
        "REDIS_URL": f"redis://:ani_dev_password@{SRV}:30453/0",
        # pgvector/object/vector components are not needed for the
        # permissions flow, but the gateway refuses to start without a
        # database; wire the server endpoints (same as the B1-B3 e2e)
        "OBJECT_STORE_PROVIDER": "minio",
        "OBJECT_STORE_ENDPOINT": f"{SRV}:30900",
        "OBJECT_STORE_PUBLIC_ENDPOINT": f"{SRV}:30900",
        "OBJECT_STORE_ACCESS_KEY_ID": "ani-s05-minio",
        "OBJECT_STORE_SECRET_ACCESS_KEY": "F36UCbnRR-bY9Upv8uuammuBwkHFlTYABiXCbtMCmlc",
        "OBJECT_STORE_SECURE": "false",
        "OBJECT_STORE_BUCKET_PREFIX": "ani-s13-",
        "OBJECT_STORE_REGION": "us-east-1",
        "VECTOR_STORE_PROVIDER": "milvus",
        "VECTOR_STORE_ENDPOINT": f"http://{SRV}:31930",
        "VECTOR_STORE_COLLECTION_PREFIX": "ani_s13_",
        "NATS_URL": f"nats://{SRV}:31062",
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
        "NATS_URL": f"nats://{SRV}:31062",
        # unique subjects so the server-deployed consumers never race us
        "NATS_PARSE_SUBJECT": f"ani.tasks.kb.parse.kbperm.{E2E_TAG}",
        "NATS_PARSE_SUBJECT_V2": f"ani.tasks.kb.parse.kbperm.v2.{E2E_TAG}",
        "ANI_GATEWAY_INTERNAL_URL": "http://localhost:8080",
        "GRPC_PORT": "50053",
        "REDIS_URL": f"redis://:ani_dev_password@{SRV}:30453/0",
        # parse consumer OFF: no doc upload in this flow; the outbox
        # dispatcher tolerates events queuing unpublished
        "KB_PARSE_CONSUMER_ENABLED": "false",
        "ANI_DEV_TENANT_ID": TENANT_ID,
    })
    return env


def start_gateway():
    _start([str(GATEWAY_EXE)], _gateway_env(),
           REPO / "services" / "ani-gateway", "gateway")


def start_kb_service():
    _start([sys.executable, "main.py"], _kb_service_env(), KB_SVC, "kb-service")


# ── REST helpers ─────────────────────────────────────────────────────────────
RESULTS: dict[str, str] = {}


def check(label: str, ok: bool, detail: str = ""):
    RESULTS[label] = "pass" if ok else "fail"
    mark = "✓" if ok else "✗"
    log(f"  {mark} {label}" + (f" — {detail}" if detail else ""))


def rest_request(method: str, path: str, body: dict | None = None,
                 expected_status: int | None = None, label: str = "",
                 timeout: float = 60.0, base: str | None = None,
                 headers: dict | None = None, desc: str = "",
                 ) -> tuple[int, Any]:
    """Send a REST request through the gateway; log purpose/input/output."""
    url = (base or REST_BASE) + path
    hdrs = dict(headers or REST_HDRS)
    data = None
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    if desc:
        log(f"[{label or method}] 接口作用: {desc}")
    log(f"[{label or method + ' ' + path}] INPUT: {method} {url}")
    if body is not None:
        log_json(f"[{label}] INPUT BODY", body)
    status = -1
    payload: Any = None
    try:
        req = urllib.request.Request(url, data=data, method=method.upper(),
                                     headers=hdrs)
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


# ── PG helpers (cleanup + async_tasks row inspection) ─────────────────────────
def _run_async(coro) -> Any:
    import asyncio
    return asyncio.run(coro)


async def _purge_kb(kb_id: str):
    """Physically purge this run's KB + permission rows (DeleteKB is a SOFT
    delete; pure e2e test data must not linger in the shared dev DB)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            await conn.execute(
                "DELETE FROM kb_permissions WHERE kb_id=$1",
                uuid.UUID(kb_id))
            await conn.execute(
                "DELETE FROM kb_documents WHERE kb_id=$1", uuid.UUID(kb_id))
            await conn.execute(
                "DELETE FROM knowledge_bases WHERE id=$1", uuid.UUID(kb_id))
            # purge this run's async_tasks (kb.create / kb.perm.update)
            await conn.execute(
                """
                DELETE FROM async_tasks
                 WHERE tenant_id=$1
                   AND (resource_id=$2::uuid
                        OR idempotency_key LIKE 'kbperm:%')
                """,
                uuid.UUID(TENANT_ID), kb_id)
            # neutralize outbox events so the server dispatcher ignores them
            await conn.execute(
                """
                UPDATE outbox_events
                   SET published = TRUE, published_at = now()
                 WHERE tenant_id=$1
                   AND payload->>'kb_id' = $2
                   AND published = FALSE
                """,
                uuid.UUID(TENANT_ID), kb_id)
        log(f"  purged KB {kb_id} + permissions + async_tasks")
    finally:
        await conn.close()


async def _count_perm_task_rows(kb_id: str) -> int:
    """Count async_tasks rows for this KB's permission updates."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            n = await conn.fetchval(
                """
                SELECT COUNT(*) FROM async_tasks
                 WHERE tenant_id=$1
                   AND task_type='kb.perm.update'
                   AND resource_id=$2::uuid
                """,
                uuid.UUID(TENANT_ID), uuid.UUID(kb_id))
            return int(n or 0)
    finally:
        await conn.close()


async def _read_perm_row(kb_id: str) -> dict | None:
    """Read the raw kb_permissions row (verify the UPSERT really persisted)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT public_read,
                       allowed_user_ids::text[] AS allowed_user_ids,
                       updated_at
                  FROM kb_permissions WHERE kb_id=$1
                """,
                uuid.UUID(kb_id))
            if row is None:
                return None
            return {
                "public_read": row["public_read"],
                "allowed_user_ids": list(row["allowed_user_ids"]),
                "updated_at": row["updated_at"].isoformat(),
            }
    finally:
        await conn.close()


# ── E2E flow ─────────────────────────────────────────────────────────────────
def run_e2e() -> int:
    # 1. CreateKB — real KB via the REST chain (explicit idempotency_key
    #    required by the gateway)
    kb_name = f"kbperm-e2e-{E2E_TAG}"
    status, kb = rest_request(
        "POST", "/knowledge-bases", body={
            "idempotency_key": str(uuid.uuid4()),
            "name": kb_name,
            "description": "B4 permissions e2e test KB",
            "embedding_model": "bge-m3",
            "chunk_size": 1024,
            "top_k": 5,
            "retrieval_mode": "hybrid",
        },
        expected_status=201, label="CreateKB",
        desc="创建测试用知识库（权限对测试的前置数据）",
        timeout=120)
    check("CreateKB -> 201", status == 201, f"status={status}")
    if status != 201 or not isinstance(kb, dict):
        return 1
    kb_id = kb.get("id", "")
    check("CreateKB body has id", bool(kb_id), f"id={kb_id}")
    if not kb_id:
        return 1

    # 2. GET permissions BEFORE any PUT → default contract values, NOT 404
    #    (kb-p1-plan §2.6: no row → public_read=false, allowed_user_ids=[],
    #     updated_at falls back to kb.created_at)
    status, perm = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/permissions",
        expected_status=200, label="GET perms (default)",
        desc="读取权限：无 kb_permissions 行时返回默认值（public_read=false, allowed_user_ids=[]），"
             "updated_at 回退 KB 创建时间；KB 不存在才 404")
    check("GET default -> 200", status == 200, f"status={status}")
    if status == 200 and isinstance(perm, dict):
        check("default public_read == False",
              perm.get("public_read") is False,
              f"public_read={perm.get('public_read')!r}")
        check("default allowed_user_ids == []",
              perm.get("allowed_user_ids") == [],
              f"allowed_user_ids={perm.get('allowed_user_ids')!r}")
        check("default updated_at == kb.created_at (no 1970)",
              perm.get("updated_at") == kb.get("created_at"),
              f"perm.updated_at={perm.get('updated_at')} kb.created_at={kb.get('created_at')}")

    # 3. PUT permissions → 200 + KB snapshot body
    idem_1 = str(uuid.uuid4())
    status, upd = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "idempotency_key": idem_1,
            "public_read": True,
            "allowed_user_ids": [USER_1, USER_2],
        },
        expected_status=200, label="PUT perms #1",
        desc="写入权限：public_read=true + 白名单两个用户；返回更新后的 KB 快照")
    check("PUT #1 -> 200", status == 200, f"status={status}")
    if status == 200 and isinstance(upd, dict):
        check("PUT #1 returns KB snapshot (id matches)",
              upd.get("id") == kb_id, f"id={upd.get('id')}")

    # 4. GET permissions → persisted values
    status, perm = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/permissions",
        expected_status=200, label="GET perms (after PUT)",
        desc="读取权限：验证 PUT 写入的值已持久化（public_read=true, 白名单两用户）")
    if status == 200 and isinstance(perm, dict):
        check("GET after PUT public_read == True",
              perm.get("public_read") is True,
              f"public_read={perm.get('public_read')!r}")
        check("GET after PUT allowed_user_ids == [u1, u2]",
              perm.get("allowed_user_ids") == [USER_1, USER_2],
              f"allowed_user_ids={perm.get('allowed_user_ids')!r}")
        check("GET after PUT updated_at > kb.created_at",
              bool(perm.get("updated_at")) and perm.get("updated_at") != kb.get("created_at"),
              f"updated_at={perm.get('updated_at')}")
        # DB-level truth: the UPSERT row really exists
        row = _run_async(_read_perm_row(kb_id))
        log_json("DB kb_permissions row", row)
        check("DB row persisted (public_read=true, 2 users)",
              row is not None and row["public_read"] is True
              and row["allowed_user_ids"] == [USER_1, USER_2],
              f"row={row}")

    # 5. PUT replay with the SAME idempotency_key → 200, no new task row
    status, upd_replay = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "idempotency_key": idem_1,       # SAME key → replay
            "public_read": True,
            "allowed_user_ids": [USER_1, USER_2],
        },
        expected_status=200, label="PUT replay (same idem key)",
        desc="幂等重放：同 idempotency_key 重发返回既有结果，不产生新的 async_tasks 行")
    check("PUT replay -> 200", status == 200, f"status={status}")
    if status == 200:
        n_tasks = _run_async(_count_perm_task_rows(kb_id))
        check("replay created NO new kb.perm.update task row",
              n_tasks == 1, f"task rows={n_tasks} (expect 1)")

    # 6. PUT with a NEW idempotency_key + NEW values → real update
    idem_2 = str(uuid.uuid4())
    status, upd2 = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "idempotency_key": idem_2,       # NEW key → real update
            "public_read": False,
            "allowed_user_ids": [USER_3],    # replace the list
        },
        expected_status=200, label="PUT perms #2",
        desc="真实更新：新 idempotency_key + 新值（public_read=false，白名单整体替换为单用户）")
    check("PUT #2 -> 200", status == 200, f"status={status}")
    status, perm2 = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/permissions",
        expected_status=200, label="GET perms (after PUT #2)",
        desc="读取权限：验证第二次 PUT 的整体替换语义（列表被替换而非合并）")
    if status == 200 and isinstance(perm2, dict):
        check("after PUT #2 public_read == False",
              perm2.get("public_read") is False,
              f"public_read={perm2.get('public_read')!r}")
        check("after PUT #2 allowed_user_ids == [u3] (replaced)",
              perm2.get("allowed_user_ids") == [USER_3],
              f"allowed_user_ids={perm2.get('allowed_user_ids')!r}")
        n_tasks = _run_async(_count_perm_task_rows(kb_id))
        check("two real updates -> 2 task rows",
              n_tasks == 2, f"task rows={n_tasks}")

    # 7. Negative: invalid uuid in allowed_user_ids → 400
    status, err = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "idempotency_key": str(uuid.uuid4()),
            "public_read": True,
            "allowed_user_ids": ["not-a-uuid"],
        },
        expected_status=400, label="PUT invalid uuid",
        desc="负例：allowed_user_ids 含非法 uuid → 400 INVALID_ARGUMENT")
    check("PUT invalid uuid -> 400", status == 400, f"status={status}")

    # 8. Negative: missing idempotency_key → 400
    status, err = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "public_read": True,
        },
        expected_status=400, label="PUT missing idem key",
        desc="负例：缺 idempotency_key → 400（Gateway 层先拦截）")
    check("PUT missing idem key -> 400", status == 400, f"status={status}")

    # 9. Negative: GET permissions on missing KB → 404
    status, err = rest_request(
        "GET", "/knowledge-bases/00000000-0000-0000-0000-999999999999/permissions",
        expected_status=404, label="GET missing KB",
        desc="负例：不存在的 KB → 404（默认值契约只适用于存在的 KB）")
    check("GET missing KB -> 404", status == 404, f"status={status}")

    # 10. Negative: cross-tenant access → 404 (RLS isolation, whole chain)
    status, err = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/permissions",
        expected_status=404, label="GET cross-tenant",
        headers=OTHER_HDRS,
        desc="负例：另一租户读本租户的 KB → 404（RLS 租户隔离贯穿整条链路）")
    check("GET cross-tenant -> 404", status == 404, f"status={status}")
    status, err = rest_request(
        "PUT", f"/knowledge-bases/{kb_id}/permissions", body={
            "idempotency_key": str(uuid.uuid4()),
            "public_read": True,
        },
        expected_status=404, label="PUT cross-tenant",
        headers=OTHER_HDRS,
        desc="负例：另一租户写本租户的 KB 权限 → 404（写路径同样被 RLS 隔离）")
    check("PUT cross-tenant -> 404", status == 404, f"status={status}")

    # 11. Cleanup: DeleteKB (soft) + direct purge
    status, _ = rest_request(
        "DELETE", f"/knowledge-bases/{kb_id}",
        expected_status=204, label="DeleteKB",
        desc="清理：软删测试 KB（随后直接物理清除，共享 dev DB 不留测试数据）")
    check("DeleteKB -> 204", status == 204, f"status={status}")
    try:
        _run_async(_purge_kb(kb_id))
    except Exception as e:
        log(f"  purge failed (acceptable — rows may cascade): {e!r}")

    return 0


# ── main ─────────────────────────────────────────────────────────────────────
def main():
    _open_log()
    log("════════════════════════════════════════════════════════════════")
    log(f"  B4 权限对 E2E：#19 GET / #20 PUT permissions — tag={E2E_TAG}")
    log(f"  tenant={TENANT_ID}")
    log(f"  log file: {E2E_LOG}")
    log("════════════════════════════════════════════════════════════════")

    if not GATEWAY_EXE.exists():
        log(f"FATAL: gateway exe not found at {GATEWAY_EXE} — run go build first")
        return 1

    log("── Killing stale processes on ports 8080/8002/50053 ──")
    _kill_stale_processes()

    try:
        # 1. start kb-service (gRPC :50053; FastAPI :8002 also comes up)
        log("── Starting kb-service (gRPC :50053) ──")
        start_kb_service()
        if not _wait_http("http://localhost:8002/health", 90):
            log("WARNING: kb-service /health not ready — continuing")

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

        # 2. start gateway
        log("── Starting ani-gateway (:8080) ──")
        start_gateway()
        if not _wait_http(
                "http://localhost:8080/api/v1/svc/knowledge-bases?limit=1", 60):
            log("FATAL: gateway not ready")
            return 1

        # warm up gateway->kb-service gRPC channel
        deadline = time.time() + 30
        warmed = False
        while time.time() < deadline:
            status, _ = rest_request(
                "GET", "/knowledge-bases?limit=1", label="grpc-channel warmup",
                desc="通道预热: 轮询直到网关→kb-service gRPC 通道就绪（非 503）",
                timeout=10)
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
        log("══════════════════════════ RESULT SUMMARY ══════════════════════════")
        log(f"  PASS: {n_pass}   FAIL: {n_fail}   total: {len(RESULTS)}")
        log("  接口覆盖: #20 PUT permissions (写入/幂等重放/替换语义) / "
            "#19 GET permissions (默认值/读回/404/跨租户)")
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
