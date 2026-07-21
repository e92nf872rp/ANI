#!/usr/bin/env python3
"""Validate Core SDKs can call the OpenAPI-driven Core mock server."""

from __future__ import annotations

from http.server import HTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
from typing import Any

from serve_core_mock import build_routes, load_spec, make_handler, server_base_path


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent


def fail(message: str) -> None:
    raise SystemExit(f"sdk mock smoke invalid: {message}")


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def import_core_sdk() -> Any:
    sys.path.insert(0, str(ROOT / "sdks/core/python"))
    import kubercloud_ani_core as sdk  # type: ignore

    return sdk


def start_mock_server() -> tuple[HTTPServer, str]:
    spec = load_spec(ROOT / "api/openapi/v1.yaml")
    base_path = server_base_path(spec)
    handler = make_handler(build_routes(spec), base_path)
    server = HTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    host, port = server.server_address
    return server, f"http://{host}:{port}{base_path}"


def validate_sdk_client_surface(sdk: Any) -> None:
    client_source = read(ROOT / "sdks/core/python/kubercloud_ani_core/client.py")
    for token in ("def request(", "urllib", "Authorization", "APIError"):
        if token not in client_source:
            fail(f"Core Python SDK client missing {token}")
    example = read(ROOT / "sdks/core/python/examples/basic.py")
    if "has_operation" not in example:
        fail("Core Python SDK example must mention has_operation")
    if not hasattr(sdk.Client(base_url="http://127.0.0.1:1"), "request"):
        fail("Core Python SDK Client must expose request()")
    ts_source = read(ROOT / "sdks/core/typescript/src/index.mjs")
    for token in ("async request(", "fetch(", "Authorization", "apiError"):
        if token not in ts_source:
            fail(f"Core TypeScript SDK client missing {token}")
    ts_example = read(ROOT / "sdks/core/typescript/examples/basic.mjs")
    if "client.request" not in ts_example:
        fail("Core TypeScript SDK example must mention client.request")
    go_source = read(ROOT / "sdks/core/go/anisdk/client.go")
    for token in ("func (client Client) Request(", "net/http", "Authorization", "APIError"):
        if token not in go_source:
            fail(f"Core Go SDK client missing {token}")
    go_example = read(ROOT / "sdks/core/go/examples/basic/main.go")
    if "apiErr.Error()" not in go_example:
        fail("Core Go SDK example must exercise APIError as error")
    java_source = read(ROOT / "sdks/core/java/src/main/java/com/kubercloud/ani/core/ApiClient.java")
    for token in (
        "java.net.http.HttpClient",
        "String request(",
        "Authorization",
        "APIException",
        "APIError",
        "Pattern.compile(\"\\\\\\\"\"",
    ):
        if token not in java_source:
            fail(f"Core Java SDK client missing {token}")
    java_example = read(ROOT / "sdks/core/java/examples/Basic.java")
    if "error.message()" not in java_example:
        fail("Core Java SDK example must exercise APIError fields")


def validate_python_mock_calls(sdk: Any) -> None:
    server, base_url = start_mock_server()
    try:
        client = sdk.Client(base_url=base_url, token="dev-token", timeout=2)
        health = client.request("GET", "/healthz")
        if not isinstance(health, dict) or health.get("status") != "ok":
            fail(f"GET /healthz returned unexpected payload: {health!r}")
        page = client.request("GET", "/instances", params=sdk.cursor_params(limit=1))
        if not isinstance(page, dict) or not isinstance(page.get("items"), list):
            fail(f"GET /instances returned unexpected payload: {page!r}")
        try:
            client.request("GET", "/missing")
        except sdk.APIError as exc:
            if exc.code != "NOT_FOUND" or not exc.request_id:
                fail(f"404 should raise APIError with NOT_FOUND and request_id, got {exc.to_dict()!r}")
        else:
            fail("GET /missing must raise APIError")
    finally:
        server.shutdown()
        server.server_close()


def validate_typescript_mock_calls() -> None:
    server, base_url = start_mock_server()
    module_url = (ROOT / "sdks/core/typescript/src/index.mjs").as_uri()
    script = f"""
import {{ Client, cursorParams }} from {module_url!r};

const client = new Client({{ baseURL: {base_url!r}, token: "dev-token" }});
const health = await client.request("GET", "/healthz");
if (!health || health.status !== "ok") {{
  throw new Error(`unexpected health payload: ${{JSON.stringify(health)}}`);
}}
const page = await client.request("GET", "/instances", {{ params: cursorParams(1) }});
if (!page || !Array.isArray(page.items)) {{
  throw new Error(`unexpected list payload: ${{JSON.stringify(page)}}`);
}}
let raised = false;
try {{
  await client.request("GET", "/missing");
}} catch (error) {{
  raised = true;
  if (!error || error.code !== "NOT_FOUND" || !error.request_id) {{
    throw new Error(`unexpected API error: ${{JSON.stringify(error)}}`);
  }}
}}
if (!raised) {{
  throw new Error("GET /missing must raise API error");
}}
"""
    try:
        with tempfile.NamedTemporaryFile("w", suffix=".mjs", encoding="utf-8", delete=False) as handle:
            handle.write(script)
            script_path = Path(handle.name)
        result = subprocess.run(["node", str(script_path)], text=True, capture_output=True, timeout=15, check=False)
        if result.returncode != 0:
            fail(f"Core TypeScript SDK mock smoke failed: {result.stderr or result.stdout}")
    finally:
        if "script_path" in locals():
            script_path.unlink(missing_ok=True)
        server.shutdown()
        server.server_close()


def validate_go_mock_calls() -> None:
    server, base_url = start_mock_server()
    module_path = ROOT / "sdks/core/go"
    script = f"""package main

import (
\t"errors"
\t"fmt"

\tanisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
)

func main() {{
\tclient := anisdk.NewClient({json.dumps(base_url)}, "dev-token")
\thealth, err := client.Request("GET", "/healthz", anisdk.RequestOptions{{}})
\tif err != nil {{
\t\tpanic(err)
\t}}
\thealthMap, ok := health.(map[string]any)
\tif !ok || healthMap["status"] != "ok" {{
\t\tpanic(fmt.Sprintf("unexpected health payload: %#v", health))
\t}}
\tpage, err := client.Request("GET", "/instances", anisdk.RequestOptions{{Params: anisdk.CursorParams(1, "")}})
\tif err != nil {{
\t\tpanic(err)
\t}}
\tpageMap, ok := page.(map[string]any)
\tif !ok {{
\t\tpanic(fmt.Sprintf("unexpected page payload: %#v", page))
\t}}
\tif _, ok := pageMap["items"].([]any); !ok {{
\t\tpanic(fmt.Sprintf("unexpected items payload: %#v", pageMap["items"]))
\t}}
\t_, err = client.Request("GET", "/missing", anisdk.RequestOptions{{}})
\tif err == nil {{
\t\tpanic("GET /missing must raise APIError")
\t}}
\tvar apiErr anisdk.APIError
\tif !errors.As(err, &apiErr) || apiErr.Code != "NOT_FOUND" || apiErr.RequestID == "" {{
\t\tpanic(fmt.Sprintf("unexpected API error: %#v", err))
\t}}
}}
"""
    try:
        with tempfile.TemporaryDirectory(prefix="ani-go-sdk-mock-") as tmp:
            tmp_path = Path(tmp)
            (tmp_path / "go.mod").write_text(
                "module ani-go-sdk-mock-smoke\n\n"
                "go 1.22\n\n"
                "require github.com/kubercloud/ani-sdks/core-go v0.0.0\n\n"
                f"replace github.com/kubercloud/ani-sdks/core-go => {module_path}\n",
                encoding="utf-8",
            )
            (tmp_path / "main.go").write_text(script, encoding="utf-8")
            env = os.environ.copy()
            env.setdefault("GOCACHE", str(ROOT / ".cache/go-build"))
            result = subprocess.run(["go", "run", "."], cwd=tmp_path, text=True, capture_output=True, timeout=30, check=False, env=env)
            if result.returncode != 0:
                fail(f"Core Go SDK mock smoke failed: {result.stderr or result.stdout}")
    finally:
        server.shutdown()
        server.server_close()


def has_jdk() -> bool:
    for command in (["javac", "-version"], ["java", "-version"]):
        try:
            result = subprocess.run(command, text=True, capture_output=True, timeout=10, check=False)
        except (FileNotFoundError, subprocess.TimeoutExpired):
            return False
        if result.returncode != 0:
            return False
    return True


def validate_java_mock_calls_or_source() -> None:
    if not has_jdk():
        return
    server, base_url = start_mock_server()
    package_path = ROOT / "sdks/core/java/src/main/java"
    client_source = ROOT / "sdks/core/java/src/main/java/com/kubercloud/ani/core/ApiClient.java"
    script = f"""import com.kubercloud.ani.core.ApiClient;

public final class JavaSdkMockSmoke {{
    public static void main(String[] args) throws Exception {{
        ApiClient client = new ApiClient({json.dumps(base_url)}, "dev-token");
        String health = client.request("GET", "/healthz", new ApiClient.RequestOptions(null, null, null));
        if (!health.contains("\\\"status\\\": \\\"ok\\\"")) {{
            throw new IllegalStateException("unexpected health payload: " + health);
        }}
        String page = client.request("GET", "/instances", new ApiClient.RequestOptions(null, ApiClient.cursorParams(1, ""), null));
        if (!page.contains("\\\"items\\\"")) {{
            throw new IllegalStateException("unexpected list payload: " + page);
        }}
        try {{
            client.request("GET", "/missing", new ApiClient.RequestOptions(null, null, null));
            throw new IllegalStateException("GET /missing must raise APIException");
        }} catch (ApiClient.APIException expected) {{
            if (!"NOT_FOUND".equals(expected.apiError().code()) || expected.apiError().requestId().isEmpty()) {{
                throw new IllegalStateException("unexpected API error: " + expected.apiError().code());
            }}
        }}
    }}
}}
"""
    try:
        with tempfile.TemporaryDirectory(prefix="ani-java-sdk-mock-") as tmp:
            tmp_path = Path(tmp)
            smoke_path = tmp_path / "JavaSdkMockSmoke.java"
            smoke_path.write_text(script, encoding="utf-8")
            result = subprocess.run(
                ["javac", "-d", str(tmp_path), str(client_source), str(smoke_path)],
                cwd=package_path,
                text=True,
                capture_output=True,
                timeout=30,
                check=False,
            )
            if result.returncode != 0:
                fail(f"Core Java SDK mock smoke compile failed: {result.stderr or result.stdout}")
            result = subprocess.run(
                ["java", "-cp", str(tmp_path), "JavaSdkMockSmoke"],
                text=True,
                capture_output=True,
                timeout=30,
                check=False,
            )
            if result.returncode != 0:
                fail(f"Core Java SDK mock smoke failed: {result.stderr or result.stdout}")
    finally:
        server.shutdown()
        server.server_close()


def validate_docs() -> None:
    required = {
        "ANI-DOCS-INDEX.md": read(DOC_ROOT / "ANI-DOCS-INDEX.md"),
        "ANI-06-开发计划.md": read(DOC_ROOT / "ANI-06-开发计划.md"),
        "CURRENT-SPRINT.md": read(ROOT / "CURRENT-SPRINT.md"),
        "development-records/README.md": read(ROOT / "development-records/README.md"),
    }
    for path, content in required.items():
        if "SDK-MOCK-SMOKE-A" not in content:
            fail(f"{path} must reference SDK-MOCK-SMOKE-A")
        if "SDK-MOCK-SMOKE-B" not in content:
            fail(f"{path} must reference SDK-MOCK-SMOKE-B")
        if "SDK-MOCK-SMOKE-C" not in content:
            fail(f"{path} must reference SDK-MOCK-SMOKE-C")
        if "SDK-MOCK-SMOKE-D" not in content:
            fail(f"{path} must reference SDK-MOCK-SMOKE-D")
    if "validate-sdk-mock-smoke" not in required["CURRENT-SPRINT.md"]:
        fail("CURRENT-SPRINT.md must list validate-sdk-mock-smoke")


def main() -> None:
    sdk = import_core_sdk()
    validate_sdk_client_surface(sdk)
    validate_python_mock_calls(sdk)
    validate_typescript_mock_calls()
    validate_go_mock_calls()
    validate_java_mock_calls_or_source()
    validate_docs()
    print("SDK mock smoke valid")


if __name__ == "__main__":
    main()
