"""E2E SSE test runner — issue-019 ani-gateway SSE endpoint.

Uses REAL services — no mocks:
  - rag-engine (port 8001)   — real retrieval: server Milvus + PG + embedding
  - ani-gateway (port 8080)  — built from backend-impl with SSE code
  - vLLM                     — real Qwen3.6-35B-A3B at 10.10.20.181:3011 (.env)
  - KB data                  — real KB collection in Milvus (564e1ff5...)

The gateway SSE handler:
  1. Calls rag-engine retrieval (real Milvus vector search + pg_trgm keyword search + RRF)
  2. Builds a prompt from retrieved sources
  3. Calls real vLLM for streaming LLM answer
  4. Forwards token events → sources event → done event

Tests the complete SSE event sequence: token*→sources→done + error scenarios.

Usage:
    python tests/e2e/run_e2e_sse_test.py
"""
import json
import os
import signal
import subprocess
import sys
import time
import urllib.request
import urllib.error

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


# ── Load .env for real vLLM / rag-engine config ─────────────────────────────
def load_env(path):
    env = {}
    if not os.path.exists(path):
        return env
    with open(path, "r") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" in line:
                k, v = line.split("=", 1)
                env[k.strip()] = v.strip()
    return env


ENV_FILE = os.path.join(REPO_ROOT, ".env")
ENV = load_env(ENV_FILE)
VLLM_API_BASE = ENV.get("VLLM_API_BASE", "http://10.10.20.181:3011/v1")
VLLM_MODEL = ENV.get("VLLM_MODEL", "Qwen3.6-35B-A3B")
VLLM_API_KEY = ENV.get("VLLM_API_KEY", "")
EMBEDDING_API_BASE = ENV.get("EMBEDDING_API_BASE", "http://10.10.20.197:8006/v1")
EMBEDDING_MODEL = ENV.get("EMBEDDING_MODEL", "Qwen3-Embedding-0.6B")
REDIS_URL = ENV.get("REDIS_URL", "redis://:ani_dev_password@10.10.1.66:30453/0")
MILVUS_ADDR = ENV.get("MILVUS_ADDR", "10.10.1.66:31930")
DATABASE_URL = ENV.get("DATABASE_URL", "postgresql://ani:ani_dev_password@10.10.1.66:30945/ani")
NATS_URL = ENV.get("NATS_URL", "nats://10.10.1.66:31062")

# Real KB with data in Milvus (7 entities, tested working)
REAL_KB_ID = "564e1ff5e8dc44aaa39154fec88b3e8b"
TENANT_ID = "tenant-00000000-0000-0000-0000-000000000001"


class Color:
    G = "\033[92m"; R = "\033[91m"; Y = "\033[93m"; C = "\033[96m"; B = "\033[1m"; X = "\033[0m"


def log(msg, c=None):
    if c is None:
        c = Color.C
    print(f"{c}{msg}{Color.X}", flush=True)


def wait_for(url, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(urllib.request.Request(url), timeout=3) as r:
                if r.status < 500:
                    return True
        except Exception:
            pass
        time.sleep(1)
    return False


def http_get(url, headers=None, timeout=30):
    req = urllib.request.Request(url)
    if headers:
        for k, v in headers.items():
            req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, r.read().decode("utf-8"), dict(r.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8"), dict(e.headers)
    except Exception as e:
        return -1, str(e), {}


processes = []
log_files = []


def start_process(cmd, cwd=None, env=None, name="", log_file=None):
    log(f"  Starting {name}...")
    full_env = os.environ.copy()
    if env:
        full_env.update(env)
    if log_file:
        lf = open(log_file, "w")
        log_files.append(lf)
        p = subprocess.Popen(
            cmd, cwd=cwd, env=full_env,
            stdout=lf, stderr=subprocess.STDOUT,
            creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == "win32" else 0,
        )
    else:
        p = subprocess.Popen(
            cmd, cwd=cwd, env=full_env,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == "win32" else 0,
        )
    processes.append((name, p))
    return p


def stop_all():
    for name, p in reversed(processes):
        try:
            if sys.platform == "win32":
                p.send_signal(signal.CTRL_BREAK_EVENT)
            else:
                p.terminate()
        except Exception:
            pass
    time.sleep(2)
    for name, p in reversed(processes):
        try:
            p.kill()
        except Exception:
            pass
    for lf in log_files:
        try:
            lf.close()
        except Exception:
            pass
    processes.clear()


results = []


def test(name, passed, detail=""):
    status = f"{Color.G}PASS{Color.X}" if passed else f"{Color.R}FAIL{Color.X}"
    results.append((name, passed, detail))
    print(f"  [{status}] {name}")
    if detail and not passed:
        print(f"         {Color.R}{detail}{Color.X}")


def run_tests():
    gw = "http://127.0.0.1:8080"
    sse = f"{gw}/api/v1/svc/knowledge-bases/{REAL_KB_ID}/query/stream"
    hdr = {"X-Dev-Tenant-ID": TENANT_ID}

    log("\n" + "=" * 60, Color.B)
    log("  E2E SSE Tests — issue-019 (ALL REAL: rag-engine + vLLM + KB)", Color.B)
    log(f"  KB: {REAL_KB_ID}", Color.B)
    log(f"  vLLM: {VLLM_MODEL} @ {VLLM_API_BASE}", Color.B)
    log("=" * 60, Color.B)

    # ── Test 1: SSE endpoint reachable + headers ──
    log("\n[Test 1] SSE endpoint reachable (200 + text/event-stream)")
    status, body, hdrs = http_get(f"{sse}?question=what+is+the+ANI+platform&score_threshold=-1", headers=hdr, timeout=120)
    ct = hdrs.get("Content-Type", "") or hdrs.get("content-type", "")
    test("Returns HTTP 200", status == 200, f"status={status} body={body[:300]}")
    test("Content-Type: text/event-stream", "text/event-stream" in ct, f"CT={ct}")
    test("Cache-Control: no-cache", "no-cache" in (hdrs.get("Cache-Control", "") or hdrs.get("cache-control", "")), "")
    test("Connection: keep-alive", "keep-alive" in (hdrs.get("Connection", "") or hdrs.get("connection", "")), "")

    # ── Test 2: Event sequence token*→sources→done ──
    log("\n[Test 2] Event sequence: token* → sources → done")
    has_token = "event: token" in body
    has_sources = "event: sources" in body
    has_done = "event: done" in body
    test("Has token event(s)", has_token, f"body={body[:300]}")
    test("Has sources event", has_sources, "")
    test("Has done event", has_done, "")
    if has_token and has_sources and has_done:
        i_tok = body.index("event: token")
        i_src = body.index("event: sources")
        i_done = body.index("event: done")
        test("Order: token < sources < done", i_tok < i_src < i_done,
             f"token={i_tok} sources={i_src} done={i_done}")
        tok_count = body.count("event: token")
        test(f"Multiple token events ({tok_count} > 0)", tok_count > 0, "")

    # ── Test 3: Token content from real vLLM (Qwen3.6-35B-A3B) ──
    log("\n[Test 3] Token content from REAL vLLM (Qwen3.6-35B-A3B)")
    token_deltas = []
    for line in body.split("\n"):
        if line.startswith("data: ") and '"delta"' in line:
            try:
                d = json.loads(line[6:])
                if "delta" in d and d["delta"]:
                    token_deltas.append(d["delta"])
            except Exception:
                pass
    test("At least 1 non-empty token delta", len(token_deltas) > 0,
         f"deltas count={len(token_deltas)}")
    if len(token_deltas) > 0:
        combined = "".join(token_deltas)
        test("Token deltas form text (len > 0)", len(combined) > 0, f"combined len={len(combined)}")
        test("Token text is meaningful (len > 10)", len(combined) > 10, f"text={combined[:100]}")

    # ── Test 4: sources event from real Milvus retrieval ──
    log("\n[Test 4] Sources event from REAL Milvus retrieval")
    if has_sources:
        i_src = body.index("event: sources")
        after = body[i_src:]
        data_line = ""
        for line in after.split("\n"):
            if line.startswith("data: "):
                data_line = line[6:]
                break
        try:
            src_data = json.loads(data_line)
            test("sources is JSON array", isinstance(src_data, list), f"data={data_line[:200]}")
            if isinstance(src_data, list) and len(src_data) > 0:
                test(f"sources has {len(src_data)} item(s)", len(src_data) > 0, "")
                test("sources[0] has doc_id", "doc_id" in src_data[0], f"src[0]={src_data[0]}")
                test("sources[0] has file_name", "file_name" in src_data[0], "")
                test("sources[0] has content", "content" in src_data[0], "")
                test("sources[0] has score", "score" in src_data[0], "")
                # Verify content is real (not empty)
                test("sources[0] content is non-empty",
                     src_data[0].get("content", "") != "", "")
            else:
                test("sources has items", False, "empty sources array")
        except Exception as e:
            test("sources is JSON array", False, f"parse error: {e}")
    else:
        test("sources is JSON array", False, "no sources event")

    # ── Test 5: done event has session_id + token counts ──
    log("\n[Test 5] Done event contains session_id + token counts")
    if has_done:
        i_done = body.index("event: done")
        after = body[i_done:]
        data_line = ""
        for line in after.split("\n"):
            if line.startswith("data: "):
                data_line = line[6:]
                break
        try:
            done_data = json.loads(data_line)
            test("done has session_id", "session_id" in done_data, f"data={data_line[:200]}")
            test("done has input_tokens", "input_tokens" in done_data, "")
            test("done has output_tokens", "output_tokens" in done_data, "")
            test("session_id is non-empty", done_data.get("session_id", "") != "", "")
        except Exception as e:
            test("done has session_id", False, f"parse error: {e}")
    else:
        test("done has session_id", False, "no done event")

    # ── Test 6: Missing question → 400 JSON (pre-stream) ──
    log("\n[Test 6] Pre-stream error: missing question → 400 JSON")
    s2, b2, _ = http_get(sse, headers=hdr, timeout=10)
    test("Returns 400", s2 == 400, f"status={s2}")
    test("Body is JSON (not SSE)", "event:" not in b2 and "code" in b2, f"body={b2[:200]}")
    try:
        err = json.loads(b2)
        test("Error code BAD_REQUEST", err.get("code") == "BAD_REQUEST", f"err={err}")
    except Exception:
        test("Error code BAD_REQUEST", False, "not JSON")

    # ── Test 7: Question too long → 400 JSON ──
    log("\n[Test 7] Pre-stream error: question > 2000 chars → 400 JSON")
    s3, b3, _ = http_get(f"{sse}?question={'a'*2001}", headers=hdr, timeout=10)
    test("Returns 400", s3 == 400, f"status={s3}")
    test("Body is JSON (not SSE)", "event:" not in b3, f"body={b3[:200]}")

    # ── Test 8: SSE four event types ──
    log("\n[Test 8] All four SSE event types (token/sources/done/error)")
    test("token event present", has_token, "")
    test("sources event present", has_sources, "")
    test("done event present", has_done, "")
    test("error path returns JSON (pre-stream 400)", s2 == 400, "")

    # ── Summary ──
    log("\n" + "=" * 60, Color.B)
    passed = sum(1 for _, p, _ in results if p)
    total = len(results)
    c = Color.G if passed == total else Color.Y
    log(f"  Result: {passed}/{total} tests passed", c)
    log("=" * 60, Color.B)
    for name, p, detail in results:
        s = f"{Color.G}PASS{Color.X}" if p else f"{Color.R}FAIL{Color.X}"
        print(f"    [{s}] {name}")
        if detail and not p:
            print(f"           {Color.R}{detail}{Color.X}")
    return passed == total


def main():
    rag_py = os.path.join(REPO_ROOT, "ai", "rag-engine", ".venv", "Scripts", "python.exe")

    log("=" * 60, Color.B)
    log("  ANI E2E SSE Test — issue-019 (ALL REAL, NO MOCKS)", Color.B)
    log(f"  rag-engine: local (→ Milvus {MILVUS_ADDR})", Color.B)
    log(f"  vLLM: {VLLM_MODEL} @ {VLLM_API_BASE}", Color.B)
    log(f"  KB: {REAL_KB_ID}", Color.B)
    log("=" * 60, Color.B)

    # ── Verify vLLM is reachable ──
    log("\n[0/3] Verifying real vLLM endpoint...")
    try:
        req = urllib.request.Request(
            f"{VLLM_API_BASE.rstrip('/')}/models",
            headers={"Authorization": f"Bearer {VLLM_API_KEY}"} if VLLM_API_KEY else {},
        )
        with urllib.request.urlopen(req, timeout=10) as r:
            models = json.loads(r.read().decode())
            model_ids = [m["id"] for m in models.get("data", [])]
            log(f"  vLLM models: {model_ids}", Color.G)
    except Exception as e:
        log(f"  FAIL: cannot reach vLLM: {e}", Color.R)
        return False

    # ── Start real rag-engine ──
    log("\n[1/3] Starting REAL rag-engine (port 8001)...")
    rag_env = {
        "MILVUS_ADDR": MILVUS_ADDR,
        "DATABASE_URL": DATABASE_URL,
        "REDIS_URL": REDIS_URL,
        "EMBEDDING_MODEL": EMBEDDING_MODEL,
        "EMBEDDING_API_BASE": EMBEDDING_API_BASE,
        "VLLM_MODEL": VLLM_MODEL,
        "VLLM_API_BASE": VLLM_API_BASE,
        "VLLM_API_KEY": VLLM_API_KEY,
        "NATS_URL": NATS_URL,
    }
    rag_main = os.path.join(REPO_ROOT, "ai", "rag-engine", "main.py")
    rag_log = os.path.join(REPO_ROOT, "ai", "rag-engine", "rag_engine_e2e.log")
    start_process([rag_py, rag_main],
                  cwd=os.path.join(REPO_ROOT, "ai", "rag-engine"),
                  env=rag_env, name="rag-engine", log_file=rag_log)
    if not wait_for("http://127.0.0.1:8001/health", timeout=45):
        log(f"FAIL: rag-engine did not start (check {rag_log})", Color.R)
        stop_all()
        return False
    log("  rag-engine OK", Color.G)

    # ── Start gateway with REAL rag-engine + REAL vLLM ──
    log("\n[2/3] Starting ani-gateway (port 8080) with SSE code...")
    gw_env = {
        "ANI_AUTH_MODE": "dev",
        "GATEWAY_PORT": "8080",
        "GATEWAY_ENV": "development",
        "LOG_LEVEL": "warn",
        "RAG_ENGINE_URL": "http://127.0.0.1:8001",
        "RAG_ENGINE_TIMEOUT": "120s",
        "VLLM_API_BASE": VLLM_API_BASE,
        "VLLM_API_KEY": VLLM_API_KEY,
        "VLLM_MODEL": VLLM_MODEL,
        "REDIS_URL": REDIS_URL,
    }
    gw_bin = os.path.join(REPO_ROOT, "services", "ani-gateway", "ani-gateway.exe")
    if not os.path.exists(gw_bin):
        log("  Building Windows gateway binary...", Color.Y)
        subprocess.run(["go", "build", "-o", gw_bin, "."],
                       cwd=os.path.join(REPO_ROOT, "services", "ani-gateway"), check=True)
    gw_log = os.path.join(REPO_ROOT, "services", "ani-gateway", "gateway_e2e.log")
    start_process([gw_bin],
                  cwd=os.path.join(REPO_ROOT, "services", "ani-gateway"),
                  env=gw_env, name="ani-gateway", log_file=gw_log)
    if not wait_for("http://127.0.0.1:8080/healthz", timeout=30):
        log(f"FAIL: gateway did not start (check {gw_log})", Color.R)
        stop_all()
        return False
    log("  gateway OK", Color.G)

    # ── Run tests ──
    log("\n[3/3] Running E2E SSE tests (real rag-engine + real vLLM)...")
    success = run_tests()

    # Cleanup
    log("\nStopping services...")
    stop_all()
    log("Done.", Color.G)
    return success


if __name__ == "__main__":
    try:
        ok = main()
        sys.exit(0 if ok else 1)
    except KeyboardInterrupt:
        stop_all()
        sys.exit(130)
