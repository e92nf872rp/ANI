#!/usr/bin/env python3
"""Generate lightweight SDK Alpha artifacts from Core and Services OpenAPI specs."""

from __future__ import annotations

from pathlib import Path
import json
import re
import shutil
from typing import Any

import yaml


LAYERS = {
    "core": {
        "spec": "api/openapi/v1.yaml",
        "go_module": "github.com/kubercloud/ani-sdks/core-go",
        "py_package": "kubercloud_ani_core",
        "java_package": "com.kubercloud.ani.core",
        "ts_name": "@kubercloud/ani-core-alpha",
    },
    "services": {
        "spec": "api/openapi/services/v1.yaml",
        "go_module": "github.com/kubercloud/ani-sdks/services-go",
        "py_package": "kubercloud_ani_services",
        "java_package": "com.kubercloud.ani.services",
        "ts_name": "@kubercloud/ani-services-alpha",
    },
}

HTTP_METHODS = {"get", "post", "put", "patch", "delete"}
KNOWN_ERROR_CODES = {
    "UNAUTHORIZED",
    "FORBIDDEN",
    "NOT_FOUND",
    "CONFLICT",
    "BAD_REQUEST",
    "RATE_LIMIT_EXCEEDED",
    "NOT_IMPLEMENTED",
    "INTERNAL_ERROR",
}
RESPONSE_ERROR_CODES = {
    "Unauthorized": "UNAUTHORIZED",
    "Forbidden": "FORBIDDEN",
    "NotFound": "NOT_FOUND",
    "BadRequest": "BAD_REQUEST",
    "Conflict": "CONFLICT",
    "RateLimitExceeded": "RATE_LIMIT_EXCEEDED",
}


def load_spec(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def collect_metadata(spec: dict[str, Any], layer: str) -> dict[str, Any]:
    operations: list[dict[str, str]] = []
    idempotency_operations: list[str] = []
    cursor_pagination_operations: list[str] = []
    schemas = spec.get("components", {}).get("schemas", {})
    for path, path_item in sorted(spec.get("paths", {}).items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            op_id = operation.get("operationId") or operation_id(method, path)
            operations.append(
                {
                    "operationId": op_id,
                    "method": method.upper(),
                    "path": path,
                    "scope": operation.get("x-ani-rbac-scope", ""),
                }
            )
            if method in {"post", "put", "patch"} and request_schema_requires_idempotency(operation, schemas):
                idempotency_operations.append(op_id)
            if method == "get" and operation_has_cursor_pagination(operation):
                cursor_pagination_operations.append(op_id)
    schema_names = sorted(schemas.keys())
    server_url = ""
    servers = spec.get("servers") or []
    if servers:
        server_url = servers[0].get("url", "")
    return {
        "layer": layer,
        "title": spec.get("info", {}).get("title", ""),
        "version": spec.get("info", {}).get("version", ""),
        "serverURL": server_url,
        "operations": operations,
        "schemas": schema_names,
        "idempotencyOperations": idempotency_operations,
        "cursorPaginationOperations": cursor_pagination_operations,
        "errorCodes": collect_error_codes(spec),
    }


def request_schema_requires_idempotency(operation: dict[str, Any], schemas: dict[str, Any]) -> bool:
    content = operation.get("requestBody", {}).get("content", {}).get("application/json", {})
    schema = content.get("schema", {})
    ref = schema.get("$ref", "")
    if not ref:
        return False
    schema_name = ref.rsplit("/", 1)[-1]
    required = set(schemas.get(schema_name, {}).get("required", []))
    return "idempotency_key" in required


def operation_has_cursor_pagination(operation: dict[str, Any]) -> bool:
    parameter_names = {parameter.get("name") for parameter in operation.get("parameters", []) if isinstance(parameter, dict)}
    return {"limit", "cursor"}.issubset(parameter_names)


def collect_error_codes(spec: dict[str, Any]) -> list[str]:
    codes: set[str] = set()
    description = spec.get("info", {}).get("description", "")
    for code in KNOWN_ERROR_CODES:
        if code in description:
            codes.add(code)
    for name, response in spec.get("components", {}).get("responses", {}).items():
        mapped = RESPONSE_ERROR_CODES.get(name)
        if mapped:
            codes.add(mapped)
        if isinstance(response, dict):
            match = re.search(r"code=([A-Z0-9_]+)", response.get("description", ""))
            if match:
                codes.add(match.group(1))
    return sorted(codes)


def operation_id(method: str, path: str) -> str:
    tokens = re.sub(r"[^a-zA-Z0-9]+", " ", path).title().replace(" ", "")
    return method.lower() + tokens


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.suffix == ".go":
        content = content.replace(
            chr(9) + '"encoding/json"' + chr(10) + chr(9) + '"encoding/hex"',
            chr(9) + '"encoding/hex"' + chr(10) + chr(9) + '"encoding/json"',
        )
    path.write_text(content, encoding="utf-8")


def generated_header(comment: str) -> str:
    return f"{comment} Code generated by scripts/gen_sdk_alpha.py; DO NOT EDIT.\n"


def go_string_list(values: list[str]) -> str:
    if not values:
        return ""
    return "\n\t" + ",\n\t".join(json.dumps(value) for value in values) + ",\n"


def generate_go(root: Path, layer: str, config: dict[str, str], metadata: dict[str, Any]) -> None:
    target = root / "sdks" / layer / "go"
    package_name = "anisdk"
    operations = [item["operationId"] for item in metadata["operations"]]
    paths = [f"{item['method']} {item['path']}" for item in metadata["operations"]]
    schemas = metadata["schemas"]
    idempotency_operations = metadata["idempotencyOperations"]
    cursor_pagination_operations = metadata["cursorPaginationOperations"]
    error_codes = metadata["errorCodes"]
    write(
        target / "go.mod",
        f"module {config['go_module']}\n\ngo 1.22\n",
    )
    write(
        target / package_name / "client.go",
        generated_header("//")
        + f"""package {package_name}

import (
\t"bytes"
\t"crypto/rand"
\t"encoding/json"
\t"encoding/hex"
\t"fmt"
\t"io"
\t"net/http"
\t"net/url"
\t"strings"
)

const Layer = {json.dumps(layer)}
const Title = {json.dumps(metadata["title"])}
const Version = {json.dumps(metadata["version"])}
const ServerURL = {json.dumps(metadata["serverURL"])}

var Operations = []string{{{go_string_list(operations)}}}
var Paths = []string{{{go_string_list(paths)}}}
var Schemas = []string{{{go_string_list(schemas)}}}
var IdempotencyOperations = []string{{{go_string_list(idempotency_operations)}}}
var CursorPaginationOperations = []string{{{go_string_list(cursor_pagination_operations)}}}
var ErrorCodes = []string{{{go_string_list(error_codes)}}}

type APIError struct {{
\tCode      string         `json:"code"`
\tMessage   string         `json:"message"`
\tRequestID string         `json:"request_id"`
\tDetails   map[string]any `json:"details,omitempty"`
}}

func (err APIError) Error() string {{
\tif err.Message == "" {{
\t\treturn err.Code
\t}}
\tif err.Code == "" {{
\t\treturn err.Message
\t}}
\treturn err.Code + ": " + err.Message
}}

type Client struct {{
\tBaseURL string
\tToken   string
}}

type RequestOptions struct {{
\tBody    map[string]any
\tParams  map[string]string
\tHeaders map[string]string
}}

func NewClient(baseURL string, token string) Client {{
\tif baseURL == "" {{
\t\tbaseURL = ServerURL
\t}}
\treturn Client{{BaseURL: baseURL, Token: token}}
}}

func (client Client) Request(method string, path string, options RequestOptions) (any, error) {{
\trequestURL, err := client.requestURL(path, options.Params)
\tif err != nil {{
\t\treturn nil, err
\t}}
\tvar body io.Reader
\tif options.Body != nil {{
\t\tpayload, err := json.Marshal(options.Body)
\t\tif err != nil {{
\t\t\treturn nil, fmt.Errorf("encode request body: %w", err)
\t\t}}
\t\tbody = bytes.NewReader(payload)
\t}}
\treq, err := http.NewRequest(strings.ToUpper(method), requestURL, body)
\tif err != nil {{
\t\treturn nil, err
\t}}
\treq.Header.Set("Accept", "application/json")
\tif body != nil {{
\t\treq.Header.Set("Content-Type", "application/json")
\t}}
\tif client.Token != "" {{
\t\treq.Header.Set("Authorization", "Bearer "+client.Token)
\t}}
\tfor key, value := range options.Headers {{
\t\treq.Header.Set(key, value)
\t}}
\tresp, err := http.DefaultClient.Do(req)
\tif err != nil {{
\t\treturn nil, err
\t}}
\tdefer resp.Body.Close()
\treturn decodeResponse(resp)
}}

func (client Client) requestURL(path string, params map[string]string) (string, error) {{
\tbase := strings.TrimRight(client.BaseURL, "/")
\trelative := "/" + strings.TrimLeft(path, "/")
\tparsed, err := url.Parse(base + relative)
\tif err != nil {{
\t\treturn "", err
\t}}
\tquery := parsed.Query()
\tfor key, value := range params {{
\t\tif value != "" {{
\t\t\tquery.Set(key, value)
\t\t}}
\t}}
\tparsed.RawQuery = query.Encode()
\treturn parsed.String(), nil
}}

func decodeResponse(resp *http.Response) (any, error) {{
\tif resp.StatusCode == http.StatusNoContent {{
\t\treturn nil, nil
\t}}
\tpayload, err := io.ReadAll(resp.Body)
\tif err != nil {{
\t\treturn nil, err
\t}}
\tif len(payload) == 0 {{
\t\tif resp.StatusCode >= 400 {{
\t\t\treturn nil, fmt.Errorf("ANI API request failed: %d", resp.StatusCode)
\t\t}}
\t\treturn nil, nil
\t}}
\tvar decoded any
\tif strings.Contains(resp.Header.Get("Content-Type"), "application/json") {{
\t\tif err := json.Unmarshal(payload, &decoded); err != nil {{
\t\t\treturn nil, err
\t\t}}
\t}} else {{
\t\tdecoded = string(payload)
\t}}
\tif resp.StatusCode >= 400 {{
\t\tif data, ok := decoded.(map[string]any); ok {{
\t\t\tcode, _ := data["code"].(string)
\t\t\tmessage, _ := data["message"].(string)
\t\t\trequestID, _ := data["request_id"].(string)
\t\t\tdetails, _ := data["details"].(map[string]any)
\t\t\tif code != "" && message != "" {{
\t\t\t\treturn nil, APIError{{Code: code, Message: message, RequestID: requestID, Details: details}}
\t\t\t}}
\t\t}}
\t\treturn nil, fmt.Errorf("ANI API request failed: %d", resp.StatusCode)
\t}}
\treturn decoded, nil
}}

func HasOperation(operationID string) bool {{
\tfor _, operation := range Operations {{
\t\tif operation == operationID {{
\t\t\treturn true
\t\t}}
\t}}
\treturn false
}}

func NewIdempotencyKey(prefix string) (string, error) {{
\tif prefix == "" {{
\t\tprefix = "ani"
\t}}
\trandom := make([]byte, 16)
\tif _, err := rand.Read(random); err != nil {{
\t\treturn "", fmt.Errorf("generate idempotency key: %w", err)
\t}}
\treturn prefix + "_" + hex.EncodeToString(random), nil
}}

func WithIdempotencyKey(body map[string]any, key string) (map[string]any, error) {{
\tif body == nil {{
\t\tbody = map[string]any{{}}
\t}}
\tif key == "" {{
\t\tgenerated, err := NewIdempotencyKey("ani")
\t\tif err != nil {{
\t\t\treturn nil, err
\t\t}}
\t\tkey = generated
\t}}
\tbody["idempotency_key"] = key
\treturn body, nil
}}

func CursorParams(limit int, cursor string) map[string]string {{
\tparams := map[string]string{{}}
\tif limit > 0 {{
\t\tparams["limit"] = fmt.Sprintf("%d", limit)
\t}}
\tif cursor != "" {{
\t\tparams["cursor"] = cursor
\t}}
\treturn params
}}

func IsAPIErrorCode(code string) bool {{
\tfor _, item := range ErrorCodes {{
\t\tif item == code {{
\t\t\treturn true
\t\t}}
\t}}
\treturn false
}}

func NewAPIError(code string, message string, requestID string, details map[string]any) APIError {{
\treturn APIError{{Code: code, Message: message, RequestID: requestID, Details: details}}
}}
""",
    )
    operation_check = ""
    if operations:
        operation_check = f"""\tif len(Operations) == 0 {{
\t\tt.Fatalf("expected generated operations")
\t}}
\tif !HasOperation({json.dumps(operations[0])}) {{
\t\tt.Fatalf("missing expected operation")
\t}}
"""
    write(
        target / package_name / "client_test.go",
        generated_header("//")
        + f"""package {package_name}

import "testing"

func TestSDKAlphaSmoke(t *testing.T) {{
\tclient := NewClient("", "token")
\tif client.BaseURL == "" || Title == "" || Version == "" {{
\t\tt.Fatalf("invalid SDK metadata")
\t}}
\tif len(Schemas) == 0 {{
\t\tt.Fatalf("expected generated schemas")
\t}}
\tkey, err := NewIdempotencyKey("test")
\tif err != nil {{
\t\tt.Fatalf("NewIdempotencyKey() error = %v", err)
\t}}
\tbody, err := WithIdempotencyKey(map[string]any{{"name": "demo"}}, key)
\tif err != nil {{
\t\tt.Fatalf("WithIdempotencyKey() error = %v", err)
\t}}
\tif body["idempotency_key"] != key {{
\t\tt.Fatalf("idempotency key helper did not set key")
\t}}
\tparams := CursorParams(20, "next")
\tif params["limit"] != "20" || params["cursor"] != "next" {{
\t\tt.Fatalf("cursor helper returned %#v", params)
\t}}
\tapiErr := NewAPIError("BAD_REQUEST", "invalid request", "req_test", nil)
\tif apiErr.Code == "" || !IsAPIErrorCode("BAD_REQUEST") {{
\t\tt.Fatalf("invalid API error helper")
\t}}
\tif apiErr.Error() == "" {{
\t\tt.Fatalf("APIError must implement error")
\t}}
\tif _, ok := any(client).(interface {{
\t\tRequest(string, string, RequestOptions) (any, error)
\t}}); !ok {{
\t\tt.Fatalf("client must expose Request")
\t}}
{operation_check}
}}
""",
    )
    write(
        target / "examples/basic/main.go",
        generated_header("//")
        + f"""package main

import (
\t"fmt"

\tanisdk {json.dumps(config["go_module"] + "/" + package_name)}
)

func main() {{
\tclient := anisdk.NewClient("http://127.0.0.1:4010/api/v1", "dev-token")
\tbody, err := anisdk.WithIdempotencyKey(map[string]any{{"name": "demo"}}, "example-key")
\tif err != nil {{
\t\tpanic(err)
\t}}
\tparams := anisdk.CursorParams(20, "")
\tapiErr := anisdk.NewAPIError("BAD_REQUEST", "invalid request", "req_example", nil)
\tif client.BaseURL == "" || body["idempotency_key"] == "" || params["limit"] != "20" || apiErr.Error() == "" || !anisdk.IsAPIErrorCode(apiErr.Code) {{
\t\tpanic("invalid SDK example")
\t}}
\tfmt.Println("{layer} go SDK example ok")
}}
""",
    )


def py_list(values: list[str]) -> str:
    return json.dumps(values, indent=4)


def generate_python(root: Path, layer: str, config: dict[str, str], metadata: dict[str, Any]) -> None:
    target = root / "sdks" / layer / "python"
    package = config["py_package"]
    operations = [item["operationId"] for item in metadata["operations"]]
    paths = [f"{item['method']} {item['path']}" for item in metadata["operations"]]
    write(
        target / package / "__init__.py",
        generated_header("#")
        + """from .client import (
    Client,
    LAYER,
    TITLE,
    VERSION,
    SERVER_URL,
    OPERATIONS,
    PATHS,
    SCHEMAS,
    IDEMPOTENCY_OPERATIONS,
    CURSOR_PAGINATION_OPERATIONS,
    ERROR_CODES,
    APIError,
    cursor_params,
    is_api_error_code,
    new_idempotency_key,
    with_idempotency_key,
)

__all__ = [
    "Client",
    "LAYER",
    "TITLE",
    "VERSION",
    "SERVER_URL",
    "OPERATIONS",
    "PATHS",
    "SCHEMAS",
    "IDEMPOTENCY_OPERATIONS",
    "CURSOR_PAGINATION_OPERATIONS",
    "ERROR_CODES",
    "APIError",
    "cursor_params",
    "is_api_error_code",
    "new_idempotency_key",
    "with_idempotency_key",
]
""",
    )
    write(
        target / package / "client.py",
        generated_header("#")
        + f'''import json
import secrets
from urllib import error, parse, request

LAYER = {json.dumps(layer)}
TITLE = {json.dumps(metadata["title"])}
VERSION = {json.dumps(metadata["version"])}
SERVER_URL = {json.dumps(metadata["serverURL"])}
OPERATIONS = {py_list(operations)}
PATHS = {py_list(paths)}
SCHEMAS = {py_list(metadata["schemas"])}
IDEMPOTENCY_OPERATIONS = {py_list(metadata["idempotencyOperations"])}
CURSOR_PAGINATION_OPERATIONS = {py_list(metadata["cursorPaginationOperations"])}
ERROR_CODES = {py_list(metadata["errorCodes"])}


class APIError(Exception):
    def __init__(self, code: str, message: str, request_id: str = "", details: dict | None = None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.request_id = request_id
        self.details = dict(details or {{}})

    def to_dict(self) -> dict:
        payload = {{"code": self.code, "message": self.message, "request_id": self.request_id}}
        if self.details:
            payload["details"] = self.details
        return payload


class Client:
    def __init__(self, base_url: str | None = None, token: str | None = None, timeout: float = 10.0):
        self.base_url = base_url or SERVER_URL
        self.token = token
        self.timeout = timeout

    def has_operation(self, operation_id: str) -> bool:
        return operation_id in OPERATIONS

    def request(
        self,
        method: str,
        path: str,
        body: dict | None = None,
        params: dict | None = None,
        headers: dict | None = None,
    ) -> dict | list | str | None:
        req = request.Request(
            self._url(path, params),
            data=self._encode_body(body),
            method=method.upper(),
            headers=self._headers(headers, body is not None),
        )
        try:
            with request.urlopen(req, timeout=self.timeout) as response:
                return self._decode_response(response.read(), response.headers.get("Content-Type", ""))
        except error.HTTPError as exc:
            payload = self._decode_response(exc.read(), exc.headers.get("Content-Type", ""))
            if isinstance(payload, dict) and "code" in payload and "message" in payload:
                raise APIError(
                    str(payload["code"]),
                    str(payload["message"]),
                    str(payload.get("request_id", "")),
                    payload.get("details") if isinstance(payload.get("details"), dict) else None,
                ) from exc
            raise

    def _url(self, path: str, params: dict | None = None) -> str:
        base = self.base_url.rstrip("/")
        relative = "/" + path.lstrip("/")
        query = parse.urlencode({{key: value for key, value in (params or {{}}).items() if value is not None}})
        return f"{{base}}{{relative}}{{'?' + query if query else ''}}"

    def _headers(self, headers: dict | None = None, has_body: bool = False) -> dict:
        merged = {{"Accept": "application/json"}}
        if has_body:
            merged["Content-Type"] = "application/json"
        if self.token:
            merged["Authorization"] = f"Bearer {{self.token}}"
        merged.update(headers or {{}})
        return merged

    def _encode_body(self, body: dict | None) -> bytes | None:
        if body is None:
            return None
        return json.dumps(body).encode("utf-8")

    def _decode_response(self, payload: bytes, content_type: str) -> dict | list | str | None:
        if not payload:
            return None
        text = payload.decode("utf-8")
        if "application/json" in content_type:
            return json.loads(text)
        return text


def new_idempotency_key(prefix: str = "ani") -> str:
    prefix = prefix or "ani"
    return f"{{prefix}}_{{secrets.token_hex(16)}}"


def with_idempotency_key(body: dict | None = None, key: str | None = None) -> dict:
    payload = dict(body or {{}})
    payload["idempotency_key"] = key or new_idempotency_key()
    return payload


def cursor_params(limit: int | None = None, cursor: str | None = None) -> dict:
    params = {{}}
    if limit is not None and limit > 0:
        params["limit"] = limit
    if cursor:
        params["cursor"] = cursor
    return params


def is_api_error_code(code: str) -> bool:
    return code in ERROR_CODES
''',
    )
    operation_check = ""
    if operations:
        operation_check = f"assert sdk.OPERATIONS\nassert client.has_operation({json.dumps(operations[0])})\n"
    write(
        target / "smoke.py",
        generated_header("#")
        + f"""import {package} as sdk

client = sdk.Client(token="token")
assert client.base_url
assert client.timeout > 0
assert sdk.TITLE
assert sdk.VERSION
assert sdk.SCHEMAS
assert sdk.new_idempotency_key("test").startswith("test_")
payload = sdk.with_idempotency_key({{"name": "demo"}}, "fixed")
assert payload["idempotency_key"] == "fixed"
assert sdk.cursor_params(20, "next") == {{"limit": 20, "cursor": "next"}}
api_error = sdk.APIError("BAD_REQUEST", "invalid request", "req_test")
assert api_error.to_dict()["code"] == "BAD_REQUEST"
assert sdk.is_api_error_code("BAD_REQUEST")
{operation_check}
print("{layer} python SDK alpha smoke ok")
""",
    )
    write(
        target / "examples/basic.py",
        generated_header("#")
        + f"""import {package} as sdk

client = sdk.Client(base_url="http://127.0.0.1:4010/api/v1", token="dev-token")
body = sdk.with_idempotency_key({{"name": "demo"}}, "example-key")
params = sdk.cursor_params(limit=20)
api_error = sdk.APIError("BAD_REQUEST", "invalid request", "req_example")

assert client.base_url
assert client.has_operation(sdk.OPERATIONS[0])
assert body["idempotency_key"] == "example-key"
assert params["limit"] == 20
assert sdk.is_api_error_code(api_error.code)

print("{layer} python SDK example ok")
""",
    )


def ts_array(values: list[str]) -> str:
    return json.dumps(values, indent=2)


def generate_typescript(root: Path, layer: str, config: dict[str, str], metadata: dict[str, Any]) -> None:
    target = root / "sdks" / layer / "typescript"
    operations = [item["operationId"] for item in metadata["operations"]]
    paths = [f"{item['method']} {item['path']}" for item in metadata["operations"]]
    idempotency_operations = metadata["idempotencyOperations"]
    error_codes = metadata["errorCodes"]
    package_json = {
        "name": config["ts_name"],
        "version": "1.0.0-alpha.0",
        "type": "module",
        "private": True,
        "exports": {"import": "./src/index.mjs", "types": "./src/index.ts"},
    }
    write(target / "package.json", json.dumps(package_json, indent=2) + "\n")
    ts_content = generated_header("//") + f"""export const layer = {json.dumps(layer)} as const;
export const title = {json.dumps(metadata["title"])} as const;
export const version = {json.dumps(metadata["version"])} as const;
export const serverURL = {json.dumps(metadata["serverURL"])} as const;
export const operations = {ts_array(operations)} as const;
export const paths = {ts_array(paths)} as const;
export const schemas = {ts_array(metadata["schemas"])} as const;
export const idempotencyOperations = {ts_array(idempotency_operations)} as const;
export const cursorPaginationOperations = {ts_array(metadata["cursorPaginationOperations"])} as const;
export const errorCodes = {ts_array(metadata["errorCodes"])} as const;

export interface ClientOptions {{
  baseURL?: string;
  token?: string;
}}

export interface RequestOptions {{
  body?: Record<string, unknown>;
  params?: Record<string, string | number | boolean | undefined>;
  headers?: Record<string, string>;
}}

export interface APIError {{
  code: string;
  message: string;
  request_id: string;
  details?: Record<string, unknown>;
}}

export class Client {{
  readonly baseURL: string;
  readonly token?: string;

  constructor(options: ClientOptions = {{}}) {{
    this.baseURL = options.baseURL || serverURL;
    this.token = options.token;
  }}

  hasOperation(operationID: string): boolean {{
    return operations.includes(operationID as typeof operations[number]);
  }}

  async request<T = unknown>(method: string, path: string, options: RequestOptions = {{}}): Promise<T> {{
    const response = await fetch(this.url(path, options.params), {{
      method: method.toUpperCase(),
      headers: this.headers(options.headers, options.body !== undefined),
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    }});
    const payload = await this.decodeResponse(response);
    if (!response.ok) {{
      if (payload && typeof payload === "object" && "code" in payload && "message" in payload) {{
        const data = payload as Record<string, unknown>;
        throw apiError(String(data.code), String(data.message), String(data.request_id || ""), data.details as Record<string, unknown> | undefined);
      }}
      throw new Error(`ANI API request failed: ${{response.status}}`);
    }}
    return payload as T;
  }}

  private url(path: string, params?: RequestOptions["params"]): string {{
    const base = this.baseURL.replace(/\\/$/, "");
    const relative = path.startsWith("/") ? path : `/${{path}}`;
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(params || {{}})) {{
      if (value !== undefined) {{
        query.set(key, String(value));
      }}
    }}
    const suffix = query.toString();
    return `${{base}}${{relative}}${{suffix ? `?${{suffix}}` : ""}}`;
  }}

  private headers(headers?: Record<string, string>, hasBody = false): Record<string, string> {{
    return {{
      Accept: "application/json",
      ...(hasBody ? {{ "Content-Type": "application/json" }} : {{}}),
      ...(this.token ? {{ Authorization: `Bearer ${{this.token}}` }} : {{}}),
      ...(headers || {{}}),
    }};
  }}

  private async decodeResponse(response: Response): Promise<unknown> {{
    if (response.status === 204) {{
      return undefined;
    }}
    const text = await response.text();
    if (!text) {{
      return undefined;
    }}
    const contentType = response.headers.get("content-type") || "";
    return contentType.includes("application/json") ? JSON.parse(text) : text;
  }}
}}

export function newIdempotencyKey(prefix = "ani"): string {{
  const safePrefix = prefix || "ani";
  const cryptoObj = globalThis.crypto;
  if (cryptoObj?.randomUUID) {{
    return `${{safePrefix}}_${{cryptoObj.randomUUID()}}`;
  }}
  const bytes = new Uint8Array(16);
  if (cryptoObj?.getRandomValues) {{
    cryptoObj.getRandomValues(bytes);
  }} else {{
    for (let i = 0; i < bytes.length; i += 1) {{
      bytes[i] = Math.floor(Math.random() * 256);
    }}
  }}
  return `${{safePrefix}}_${{Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}}`;
}}

export function withIdempotencyKey<T extends Record<string, unknown>>(body: T, key = newIdempotencyKey()): T & {{ idempotency_key: string }} {{
  return {{ ...body, idempotency_key: key }};
}}

export function cursorParams(limit?: number, cursor?: string): Record<string, string> {{
  const params: Record<string, string> = {{}};
  if (limit !== undefined && limit > 0) {{
    params.limit = String(limit);
  }}
  if (cursor) {{
    params.cursor = cursor;
  }}
  return params;
}}

export function isAPIErrorCode(code: string): boolean {{
  return errorCodes.includes(code as typeof errorCodes[number]);
}}

export function apiError(code: string, message: string, requestID = "", details?: Record<string, unknown>): APIError {{
  return {{ code, message, request_id: requestID, ...(details ? {{ details }} : {{}}) }};
}}
"""
    write(target / "src" / "index.ts", ts_content)
    js_content = generated_header("//") + f"""export const layer = {json.dumps(layer)};
export const title = {json.dumps(metadata["title"])};
export const version = {json.dumps(metadata["version"])};
export const serverURL = {json.dumps(metadata["serverURL"])};
export const operations = {json.dumps(operations, indent=2)};
export const paths = {json.dumps(paths, indent=2)};
export const schemas = {json.dumps(metadata["schemas"], indent=2)};
export const idempotencyOperations = {json.dumps(idempotency_operations, indent=2)};
export const cursorPaginationOperations = {json.dumps(metadata["cursorPaginationOperations"], indent=2)};
export const errorCodes = {json.dumps(metadata["errorCodes"], indent=2)};

export class Client {{
  constructor(options = {{}}) {{
    this.baseURL = options.baseURL || serverURL;
    this.token = options.token;
  }}

  hasOperation(operationID) {{
    return operations.includes(operationID);
  }}

  async request(method, path, options = {{}}) {{
    const response = await fetch(this.url(path, options.params), {{
      method: method.toUpperCase(),
      headers: this.headers(options.headers, options.body !== undefined),
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    }});
    const payload = await this.decodeResponse(response);
    if (!response.ok) {{
      if (payload && typeof payload === "object" && "code" in payload && "message" in payload) {{
        throw apiError(String(payload.code), String(payload.message), String(payload.request_id || ""), payload.details);
      }}
      throw new Error(`ANI API request failed: ${{response.status}}`);
    }}
    return payload;
  }}

  url(path, params = {{}}) {{
    const base = this.baseURL.replace(/\\/$/, "");
    const relative = path.startsWith("/") ? path : `/${{path}}`;
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {{
      if (value !== undefined) {{
        query.set(key, String(value));
      }}
    }}
    const suffix = query.toString();
    return `${{base}}${{relative}}${{suffix ? `?${{suffix}}` : ""}}`;
  }}

  headers(headers = {{}}, hasBody = false) {{
    return {{
      Accept: "application/json",
      ...(hasBody ? {{ "Content-Type": "application/json" }} : {{}}),
      ...(this.token ? {{ Authorization: `Bearer ${{this.token}}` }} : {{}}),
      ...headers,
    }};
  }}

  async decodeResponse(response) {{
    if (response.status === 204) {{
      return undefined;
    }}
    const text = await response.text();
    if (!text) {{
      return undefined;
    }}
    const contentType = response.headers.get("content-type") || "";
    return contentType.includes("application/json") ? JSON.parse(text) : text;
  }}
}}

export function newIdempotencyKey(prefix = "ani") {{
  const safePrefix = prefix || "ani";
  const cryptoObj = globalThis.crypto;
  if (cryptoObj?.randomUUID) {{
    return `${{safePrefix}}_${{cryptoObj.randomUUID()}}`;
  }}
  const bytes = new Uint8Array(16);
  if (cryptoObj?.getRandomValues) {{
    cryptoObj.getRandomValues(bytes);
  }} else {{
    for (let i = 0; i < bytes.length; i += 1) {{
      bytes[i] = Math.floor(Math.random() * 256);
    }}
  }}
  return `${{safePrefix}}_${{Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}}`;
}}

export function withIdempotencyKey(body = {{}}, key = newIdempotencyKey()) {{
  return {{ ...body, idempotency_key: key }};
}}

export function cursorParams(limit, cursor) {{
  const params = {{}};
  if (limit !== undefined && limit > 0) {{
    params.limit = String(limit);
  }}
  if (cursor) {{
    params.cursor = cursor;
  }}
  return params;
}}

export function isAPIErrorCode(code) {{
  return errorCodes.includes(code);
}}

export function apiError(code, message, requestID = "", details) {{
  return {{ code, message, request_id: requestID, ...(details ? {{ details }} : {{}}) }};
}}
"""
    write(target / "src" / "index.mjs", js_content)
    operation_check = ""
    if operations:
        operation_check = f"""if (operations.length === 0) {{
  throw new Error("expected generated operations");
}}
if (!client.hasOperation({json.dumps(operations[0])})) {{
  throw new Error("missing expected operation");
}}
"""
    write(
        target / "smoke.mjs",
        generated_header("//")
        + f"""import {{ Client, apiError, cursorParams, isAPIErrorCode, newIdempotencyKey, operations, schemas, title, version, withIdempotencyKey }} from "./src/index.mjs";

const client = new Client({{ token: "token" }});
if (!client.baseURL || !title || !version || schemas.length === 0) {{
  throw new Error("invalid SDK metadata");
}}
if (!newIdempotencyKey("test").startsWith("test_")) {{
  throw new Error("invalid idempotency key helper");
}}
if (withIdempotencyKey({{ name: "demo" }}, "fixed").idempotency_key !== "fixed") {{
  throw new Error("invalid idempotency payload helper");
}}
const params = cursorParams(20, "next");
if (params.limit !== "20" || params.cursor !== "next") {{
  throw new Error("invalid cursor pagination helper");
}}
const error = apiError("BAD_REQUEST", "invalid request", "req_test");
if (error.code !== "BAD_REQUEST" || !isAPIErrorCode("BAD_REQUEST")) {{
  throw new Error("invalid API error helper");
}}
if (typeof client.request !== "function") {{
  throw new Error("missing SDK HTTP request helper");
}}
{operation_check}
console.log("{layer} typescript SDK alpha smoke ok");
""",
    )
    write(
        target / "examples/basic.mjs",
        generated_header("//")
        + f"""import {{ Client, apiError, cursorParams, isAPIErrorCode, withIdempotencyKey }} from "../src/index.mjs";

const client = new Client({{ baseURL: "http://127.0.0.1:4010/api/v1", token: "dev-token" }});
const body = withIdempotencyKey({{ name: "demo" }}, "example-key");
const params = cursorParams(20);
const error = apiError("BAD_REQUEST", "invalid request", "req_example");

if (!client.baseURL || typeof client.request !== "function" || body.idempotency_key !== "example-key" || params.limit !== "20" || !isAPIErrorCode(error.code)) {{
  throw new Error("invalid SDK example");
}}

console.log("{layer} typescript SDK example ok");
""",
    )


def java_string_array(values: list[str]) -> str:
    if not values:
        return ""
    return "\n        " + ",\n        ".join(json.dumps(value) for value in values) + "\n    "


def generate_java(root: Path, layer: str, config: dict[str, str], metadata: dict[str, Any]) -> None:
    target = root / "sdks" / layer / "java"
    package = config["java_package"]
    package_path = Path(*package.split("."))
    smoke_base_url = "http://127.0.0.1:4010/api/v1" + ("/svc" if layer == "services" else "")
    operations = [item["operationId"] for item in metadata["operations"]]
    paths = [f"{item['method']} {item['path']}" for item in metadata["operations"]]
    idempotency_operations = metadata["idempotencyOperations"]
    error_codes = metadata["errorCodes"]
    write(
        target / "src/main/java" / package_path / "ApiClient.java",
        generated_header("//")
        + f"""package {package};

import java.security.SecureRandom;
import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.StringJoiner;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public final class ApiClient {{
    private static final HttpClient HTTP = HttpClient.newHttpClient();
    private static final SecureRandom RANDOM = new SecureRandom();
    public static final String LAYER = {json.dumps(layer)};
    public static final String TITLE = {json.dumps(metadata["title"])};
    public static final String VERSION = {json.dumps(metadata["version"])};
    public static final String SERVER_URL = {json.dumps(metadata["serverURL"])};
    public static final List<String> OPERATIONS = Collections.unmodifiableList(Arrays.asList({java_string_array(operations)}));
    public static final List<String> PATHS = Collections.unmodifiableList(Arrays.asList({java_string_array(paths)}));
    public static final List<String> SCHEMAS = Collections.unmodifiableList(Arrays.asList({java_string_array(metadata["schemas"])}));
    public static final List<String> IDEMPOTENCY_OPERATIONS = Collections.unmodifiableList(Arrays.asList({java_string_array(idempotency_operations)}));
    public static final List<String> CURSOR_PAGINATION_OPERATIONS = Collections.unmodifiableList(Arrays.asList({java_string_array(metadata["cursorPaginationOperations"])}));
    public static final List<String> ERROR_CODES = Collections.unmodifiableList(Arrays.asList({java_string_array(error_codes)}));

    private final String baseUrl;
    private final String token;

    public static final class APIError {{
        private final String code;
        private final String message;
        private final String requestId;
        private final Map<String, Object> details;

        public APIError(String code, String message, String requestId, Map<String, Object> details) {{
            this.code = code;
            this.message = message;
            this.requestId = requestId;
            this.details = details == null ? Collections.emptyMap() : Collections.unmodifiableMap(new HashMap<>(details));
        }}

        public String code() {{
            return code;
        }}

        public String message() {{
            return message;
        }}

        public String requestId() {{
            return requestId;
        }}

        public Map<String, Object> details() {{
            return details;
        }}
    }}

    public static final class APIException extends Exception {{
        private final APIError apiError;

        public APIException(APIError apiError) {{
            super(apiError == null ? "" : apiError.code() + ": " + apiError.message());
            this.apiError = apiError;
        }}

        public APIError apiError() {{
            return apiError;
        }}
    }}

    public static final class RequestOptions {{
        private final String bodyJson;
        private final Map<String, String> params;
        private final Map<String, String> headers;

        public RequestOptions(String bodyJson, Map<String, String> params, Map<String, String> headers) {{
            this.bodyJson = bodyJson;
            this.params = params == null ? Collections.emptyMap() : Collections.unmodifiableMap(new HashMap<>(params));
            this.headers = headers == null ? Collections.emptyMap() : Collections.unmodifiableMap(new HashMap<>(headers));
        }}

        public String bodyJson() {{
            return bodyJson;
        }}

        public Map<String, String> params() {{
            return params;
        }}

        public Map<String, String> headers() {{
            return headers;
        }}
    }}

    public ApiClient(String baseUrl, String token) {{
        this.baseUrl = (baseUrl == null || baseUrl.isEmpty()) ? SERVER_URL : baseUrl;
        this.token = token;
    }}

    public String baseUrl() {{
        return baseUrl;
    }}

    public String token() {{
        return token;
    }}

    public boolean hasOperation(String operationId) {{
        return OPERATIONS.contains(operationId);
    }}

    public String request(String method, String path, RequestOptions options) throws IOException, InterruptedException, APIException {{
        RequestOptions opts = options == null ? new RequestOptions(null, null, null) : options;
        HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(requestUrl(path, opts.params())))
                .header("Accept", "application/json");
        if (token != null && !token.isEmpty()) {{
            builder.header("Authorization", "Bearer " + token);
        }}
        for (Map.Entry<String, String> entry : opts.headers().entrySet()) {{
            builder.header(entry.getKey(), entry.getValue());
        }}
        String upperMethod = method == null ? "GET" : method.toUpperCase();
        if (opts.bodyJson() == null) {{
            builder.method(upperMethod, HttpRequest.BodyPublishers.noBody());
        }} else {{
            builder.header("Content-Type", "application/json");
            builder.method(upperMethod, HttpRequest.BodyPublishers.ofString(opts.bodyJson()));
        }}
        HttpResponse<String> response = HTTP.send(builder.build(), HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() >= 400) {{
            throw new APIException(parseAPIError(response.body(), response.statusCode()));
        }}
        return response.body();
    }}

    private String requestUrl(String path, Map<String, String> params) {{
        String base = baseUrl.replaceAll("/+$", "");
        String relative = path == null || path.isEmpty() ? "/" : "/" + path.replaceAll("^/+", "");
        if (params == null || params.isEmpty()) {{
            return base + relative;
        }}
        StringJoiner query = new StringJoiner("&");
        for (Map.Entry<String, String> entry : params.entrySet()) {{
            if (entry.getValue() != null && !entry.getValue().isEmpty()) {{
                query.add(URLEncoder.encode(entry.getKey(), StandardCharsets.UTF_8) + "=" + URLEncoder.encode(entry.getValue(), StandardCharsets.UTF_8));
            }}
        }}
        String encoded = query.toString();
        return encoded.isEmpty() ? base + relative : base + relative + "?" + encoded;
    }}

    private static APIError parseAPIError(String body, int statusCode) {{
        String code = jsonString(body, "code");
        String message = jsonString(body, "message");
        String requestId = jsonString(body, "request_id");
        if (code == null || code.isEmpty()) {{
            code = "HTTP_" + statusCode;
        }}
        if (message == null || message.isEmpty()) {{
            message = "ANI API request failed";
        }}
        return new APIError(code, message, requestId == null ? "" : requestId, null);
    }}

    private static String jsonString(String body, String field) {{
        if (body == null) {{
            return "";
        }}
        Pattern pattern = Pattern.compile("\\\\\\\"" + Pattern.quote(field) + "\\\\\\\"\\\\s*:\\\\s*\\\\\\\"([^\\\\\\\"]*)\\\\\\\"");
        Matcher matcher = pattern.matcher(body);
        return matcher.find() ? matcher.group(1) : "";
    }}

    public static String newIdempotencyKey(String prefix) {{
        String safePrefix = (prefix == null || prefix.isEmpty()) ? "ani" : prefix;
        byte[] random = new byte[16];
        RANDOM.nextBytes(random);
        StringBuilder encoded = new StringBuilder(random.length * 2);
        for (byte value : random) {{
            encoded.append(String.format("%02x", value & 0xff));
        }}
        return safePrefix + "_" + encoded;
    }}

    public static Map<String, Object> withIdempotencyKey(Map<String, Object> body, String key) {{
        Map<String, Object> payload = new HashMap<>();
        if (body != null) {{
            payload.putAll(body);
        }}
        payload.put("idempotency_key", (key == null || key.isEmpty()) ? newIdempotencyKey("ani") : key);
        return payload;
    }}

    public static Map<String, String> cursorParams(int limit, String cursor) {{
        Map<String, String> params = new HashMap<>();
        if (limit > 0) {{
            params.put("limit", Integer.toString(limit));
        }}
        if (cursor != null && !cursor.isEmpty()) {{
            params.put("cursor", cursor);
        }}
        return params;
    }}

    public static boolean isAPIErrorCode(String code) {{
        return ERROR_CODES.contains(code);
    }}

    public static APIError apiError(String code, String message, String requestId, Map<String, Object> details) {{
        return new APIError(code, message, requestId, details);
    }}
}}
""",
    )
    operation_check = ""
    if operations:
        operation_check = f"""        if (ApiClient.OPERATIONS.isEmpty()) {{
            throw new IllegalStateException("expected generated operations");
        }}
        if (!client.hasOperation({json.dumps(operations[0])})) {{
            throw new IllegalStateException("missing expected operation");
        }}
"""
    write(
        target / "src/test/java" / package_path / "Smoke.java",
        generated_header("//")
        + f"""package {package};

public final class Smoke {{
    public static void main(String[] args) {{
        ApiClient client = new ApiClient({json.dumps(smoke_base_url)}, "token");
        if (client.baseUrl().isEmpty() || ApiClient.TITLE.isEmpty() || ApiClient.VERSION.isEmpty()) {{
            throw new IllegalStateException("invalid SDK metadata");
        }}
        if (ApiClient.SCHEMAS.isEmpty()) {{
            throw new IllegalStateException("expected generated schemas");
        }}
        if (!ApiClient.newIdempotencyKey("test").startsWith("test_")) {{
            throw new IllegalStateException("invalid idempotency key helper");
        }}
        if (!"fixed".equals(ApiClient.withIdempotencyKey(null, "fixed").get("idempotency_key"))) {{
            throw new IllegalStateException("invalid idempotency payload helper");
        }}
        if (!"20".equals(ApiClient.cursorParams(20, "next").get("limit"))) {{
            throw new IllegalStateException("invalid cursor pagination helper");
        }}
        ApiClient.APIError error = ApiClient.apiError("BAD_REQUEST", "invalid request", "req_test", null);
        if (!"BAD_REQUEST".equals(error.code()) || !ApiClient.isAPIErrorCode("BAD_REQUEST")) {{
            throw new IllegalStateException("invalid API error helper");
        }}
        try {{
            client.request("GET", "/healthz", new ApiClient.RequestOptions(null, null, null));
        }} catch (java.io.IOException | InterruptedException | ApiClient.APIException expectedWithoutServer) {{
            // Source smoke only checks that the request surface is callable without external dependencies.
        }}
{operation_check}
        System.out.println("{layer} java SDK alpha smoke ok");
    }}
}}
""",
    )
    write(
        target / "examples/Basic.java",
        generated_header("//")
        + f"""import {package}.ApiClient;

public final class Basic {{
    public static void main(String[] args) {{
        ApiClient client = new ApiClient("http://127.0.0.1:4010/api/v1", "dev-token");
        java.util.Map<String, Object> body = ApiClient.withIdempotencyKey(null, "example-key");
        java.util.Map<String, String> params = ApiClient.cursorParams(20, "");
        ApiClient.APIError error = ApiClient.apiError("BAD_REQUEST", "invalid request", "req_example", null);
        if (client.baseUrl().isEmpty()
                || !"example-key".equals(body.get("idempotency_key"))
                || !"20".equals(params.get("limit"))
                || error.message().isEmpty()
                || !ApiClient.isAPIErrorCode(error.code())) {{
            throw new IllegalStateException("invalid SDK example");
        }}
        System.out.println("{layer} java SDK example ok");
    }}
}}
""",
    )


def generate_layer(root: Path, layer: str, config: dict[str, str]) -> None:
    metadata = collect_metadata(load_spec(root / config["spec"]), layer)
    layer_root = root / "sdks" / layer
    if layer_root.exists():
        shutil.rmtree(layer_root)
    write(layer_root / "sdk-metadata.json", json.dumps(metadata, indent=2) + "\n")
    generate_go(root, layer, config, metadata)
    generate_python(root, layer, config, metadata)
    generate_typescript(root, layer, config, metadata)
    generate_java(root, layer, config, metadata)


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    for layer, config in LAYERS.items():
        generate_layer(root, layer, config)
    print("SDK Alpha artifacts generated")


if __name__ == "__main__":
    main()
