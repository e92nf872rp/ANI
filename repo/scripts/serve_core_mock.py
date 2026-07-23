#!/usr/bin/env python3
"""Serve a lightweight Core API mock from the OpenAPI contract.

This is a dependency-free local fallback for Sprint 4 MOCK-A. It keeps the
mock behavior driven by api/openapi/v1.yaml so a future Prism entrypoint can
replace the server without changing the contract checks.
"""

from __future__ import annotations

import argparse
import base64
import datetime
import hashlib
import json
import os
import re
import socket
import struct
import threading
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from urllib.parse import urlparse, parse_qs
from typing import Any

import yaml


HTTP_METHODS = {"get", "post", "put", "patch", "delete"}
ROOT = Path(__file__).resolve().parents[1]
DEFAULT_SPEC = ROOT / "api/openapi/v1.yaml"


@dataclass(frozen=True)
class MockRoute:
    method: str
    path_template: str
    operation_id: str
    status_code: int
    content_type: str
    body: Any
    pattern: re.Pattern[str]


def load_spec(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        raise ValueError(f"{path} must parse to an object")
    return data


def server_base_path(spec: dict[str, Any]) -> str:
    servers = spec.get("servers") or []
    if not servers:
        return ""
    path = urlparse(servers[0].get("url", "")).path.rstrip("/")
    return path if path != "/" else ""


def build_routes(spec: dict[str, Any]) -> list[MockRoute]:
    routes: list[MockRoute] = []
    for path, path_item in sorted(spec.get("paths", {}).items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            response = choose_success_response(spec, operation)
            status_code = response["status_code"]
            media = response.get("media") or {}
            schema = media.get("schema", {})
            content_type = response.get("content_type") or "application/json"
            body = None if status_code == 204 else mock_value(spec, schema, operation.get("operationId", "mock"))
            routes.append(
                MockRoute(
                    method=method.upper(),
                    path_template=path,
                    operation_id=operation.get("operationId") or fallback_operation_id(method, path),
                    status_code=status_code,
                    content_type=content_type,
                    body=body,
                    pattern=compile_path(path),
                )
            )
    return routes


def choose_success_response(spec: dict[str, Any], operation: dict[str, Any]) -> dict[str, Any]:
    responses = operation.get("responses", {})
    if not isinstance(responses, dict):
        raise ValueError(f"{operation.get('operationId', '<unknown>')} missing responses")
    for status in sorted(responses):
        if not str(status).startswith("2"):
            continue
        response = resolve_ref(spec, responses[status])
        content = response.get("content", {}) if isinstance(response, dict) else {}
        if "application/json" in content:
            return {
                "status_code": int(status),
                "content_type": "application/json",
                "media": content["application/json"],
            }
        if str(status) == "204":
            return {"status_code": 204, "content_type": "", "media": {}}
        first_content_type = next(iter(content), "application/json")
        return {
            "status_code": int(status),
            "content_type": first_content_type,
            "media": content.get(first_content_type, {}),
        }
    raise ValueError(f"{operation.get('operationId', '<unknown>')} missing 2xx response")


def mock_value(spec: dict[str, Any], schema: dict[str, Any], operation_id: str) -> Any:
    schema = resolve_ref(spec, schema or {})
    if not isinstance(schema, dict) or not schema:
        return {"operation_id": operation_id, "mock": True}
    if "example" in schema:
        return schema["example"]
    if "enum" in schema and schema["enum"]:
        return schema["enum"][0]
    if "oneOf" in schema:
        return mock_value(spec, schema["oneOf"][0], operation_id)
    if "anyOf" in schema:
        return mock_value(spec, schema["anyOf"][0], operation_id)
    if "allOf" in schema:
        merged: dict[str, Any] = {}
        for item in schema["allOf"]:
            value = mock_value(spec, item, operation_id)
            if isinstance(value, dict):
                merged.update(value)
        return merged
    schema_type = schema.get("type")
    if isinstance(schema_type, list):
        schema_type = next((item for item in schema_type if item != "null"), schema_type[0])
    if schema_type == "object" or "properties" in schema:
        properties = schema.get("properties", {})
        if not properties:
            return {}
        return {name: mock_value(spec, child, operation_id) for name, child in properties.items()}
    if schema_type == "array":
        return [mock_value(spec, schema.get("items", {}), operation_id)]
    if schema_type == "integer":
        return 1
    if schema_type == "number":
        return 1.0
    if schema_type == "boolean":
        return True
    if schema.get("format") == "uuid":
        return "00000000-0000-4000-8000-000000000001"
    if schema.get("format") == "date-time":
        return "2026-05-20T00:00:00Z"
    return "mock"


def resolve_ref(spec: dict[str, Any], value: Any) -> Any:
    if not isinstance(value, dict) or "$ref" not in value:
        return value
    ref = value["$ref"]
    if not ref.startswith("#/"):
        raise ValueError(f"unsupported external ref {ref}")
    current: Any = spec
    for part in ref[2:].split("/"):
        current = current[part]
    return current


def compile_path(path: str) -> re.Pattern[str]:
    escaped = re.escape(path)
    pattern = re.sub(r"\\\{[^/]+\\\}", r"[^/]+", escaped)
    return re.compile("^" + pattern + "$")


def fallback_operation_id(method: str, path: str) -> str:
    tokens = re.sub(r"[^a-zA-Z0-9]+", " ", path).title().replace(" ", "")
    return method.lower() + tokens


def find_route(routes: list[MockRoute], method: str, path: str, base_path: str) -> MockRoute | None:
    candidate = path
    if base_path and candidate.startswith(base_path + "/"):
        candidate = candidate[len(base_path) :]
    for route in routes:
        if route.method == method and route.pattern.match(candidate):
            return route
    return None


def _extract_instance_id(path: str, base_path: str) -> str:
    """从 /api/v1/instances/{instance_id}/... 路径中提取 instance_id。"""
    candidate = path
    if base_path and candidate.startswith(base_path + "/"):
        candidate = candidate[len(base_path) :]
    # candidate 形如 /instances/{instance_id}/metrics 或 /instances/{instance_id}
    parts = candidate.split("/")
    # parts = ['', 'instances', '{instance_id}', ...]
    if len(parts) >= 3 and parts[1] == "instances":
        return parts[2]
    return ""


def mock_logs_response(query: dict[str, list[str]]) -> dict[str, Any]:
    """为 listInstanceLogs 返回多条不同 level 的日志，并响应 level 过滤与 limit。

    便于 Console 日志 Tab 在本地 mock 下验证级别筛选与 cursor 分页。
    """
    all_items = [
        {"timestamp": "2026-05-20T00:00:00Z", "level": "info", "message": "instance runtime is running", "container": "main", "stream": "stdout"},
        {"timestamp": "2026-05-20T00:00:05Z", "level": "warn", "message": "local profile does not attach to a real provider log stream", "container": "main", "stream": "stderr"},
        {"timestamp": "2026-05-20T00:00:10Z", "level": "debug", "message": "dev_profile observation generated by local adapter", "container": "sidecar", "stream": "stdout"},
        {"timestamp": "2026-05-20T00:00:15Z", "level": "error", "message": "failed to attach volume pvc-demo", "container": "main", "stream": "stderr"},
        {"timestamp": "2026-05-20T00:00:20Z", "level": "info", "message": "health check passed", "container": "main", "stream": "stdout"},
    ]
    level = (query.get("level") or [""])[0]
    items = [it for it in all_items if not level or it["level"] == level]
    limit_raw = (query.get("limit") or ["100"])[0]
    try:
        limit = int(limit_raw)
    except ValueError:
        limit = 100
    items = items[:limit]
    return {
        "items": items,
        "total": len(items),
        "next_cursor": None,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


def mock_events_response(query: dict[str, list[str]]) -> dict[str, Any]:
    """为 listInstanceEvents 返回多条 Normal/Warning 事件，并响应 type 过滤与 limit。

    便于 Console 事件 Tab 在本地 mock 下验证类型筛选、空态、loading、列渲染。
    """
    all_items = [
        {"id": "evt-001", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Normal", "reason": "Started", "message": "instance started successfully", "count": 1, "occurred_at": "2026-05-20T00:00:00Z"},
        {"id": "evt-002", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Normal", "reason": "Pulled", "message": "Successfully pulled image nginx:1.27", "count": 1, "occurred_at": "2026-05-20T00:00:10Z"},
        {"id": "evt-003", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Warning", "reason": "FailedScheduling", "message": "0/3 nodes are available: insufficient cpu", "count": 3, "occurred_at": "2026-05-20T00:00:20Z"},
        {"id": "evt-004", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Normal", "reason": "Created", "message": "Created container main", "count": 1, "occurred_at": "2026-05-20T00:00:30Z"},
        {"id": "evt-005", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Warning", "reason": "BackOff", "message": "Back-off restarting failed container", "count": 5, "occurred_at": "2026-05-20T00:00:40Z"},
        {"id": "evt-006", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Normal", "reason": "Healthy", "message": "readiness probe passed", "count": 12, "occurred_at": "2026-05-20T00:00:50Z"},
        {"id": "evt-007", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Warning", "reason": "Unhealthy", "message": "liveness probe failed: connection refused", "count": 2, "occurred_at": "2026-05-20T00:01:00Z"},
        {"id": "evt-008", "instance_id": "00000000-0000-4000-8000-000000000001", "type": "Normal", "reason": "Killing", "message": "Stopping container main", "count": 1, "occurred_at": "2026-05-20T00:01:10Z"},
    ]
    type_filter = (query.get("type") or [""])[0]
    items = [it for it in all_items if not type_filter or it["type"] == type_filter]
    limit_raw = (query.get("limit") or ["100"])[0]
    try:
        limit = int(limit_raw)
    except ValueError:
        limit = 100
    items = items[:limit]
    return {
        "items": items,
        "total": len(items),
        "next_cursor": None,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


def mock_security_events_response(query: dict[str, list[str]]) -> dict[str, Any]:
    """为 listInstanceSecurityEvents 返回多条不同 severity 的安全事件，并响应 severity 过滤与 limit。

    便于 Console 安全事件 Tab（issue-009）在本地 mock 下验证 severity 筛选、空态、loading、列渲染。
    """
    all_items = [
        {"id": "sev-001", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "sandbox_escape_attempt", "severity": "critical", "description": "检测到沙箱进程尝试访问宿主机文件系统路径 /etc/passwd，已阻断。", "occurred_at": "2026-05-20T00:00:00Z"},
        {"id": "sev-002", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "privilege_escalation", "severity": "critical", "description": "容器内进程尝试通过 cap_sys_admin 提权，被 seccomp 策略拦截。", "occurred_at": "2026-05-20T00:00:10Z"},
        {"id": "sev-003", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "suspicious_network_egress", "severity": "warning", "description": "沙箱外连到非常规端口 4444，疑似 C2 通信，建议核查。", "occurred_at": "2026-05-20T00:00:20Z"},
        {"id": "sev-004", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "resource_threshold_exceeded", "severity": "warning", "description": "沙箱内存使用率 92%，接近 hard limit，可能触发 OOMKill。", "occurred_at": "2026-05-20T00:00:30Z"},
        {"id": "sev-005", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "syscall_policy_violation", "severity": "warning", "description": "进程调用被禁用的 syscall (keyctl)，已记录审计。", "occurred_at": "2026-05-20T00:00:40Z"},
        {"id": "sev-006", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "sandbox_started", "severity": "info", "description": "沙箱实例已启动，应用 seccomp profile: ani-default-deny。", "occurred_at": "2026-05-20T00:00:50Z"},
        {"id": "sev-007", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "health_check_passed", "severity": "info", "description": "健康检查通过，readiness probe 200 OK。", "occurred_at": "2026-05-20T00:01:00Z"},
        {"id": "sev-008", "instance_id": "00000000-0000-4000-8000-000000000005", "event_type": "session_opened", "severity": "info", "description": "用户 dev@ani.local 打开 exec 会话，来源 IP 127.0.0.1。", "occurred_at": "2026-05-20T00:01:10Z"},
    ]
    severity = (query.get("severity") or [""])[0]
    items = [it for it in all_items if not severity or it["severity"] == severity]
    limit_raw = (query.get("limit") or ["100"])[0]
    try:
        limit = int(limit_raw)
    except ValueError:
        limit = 100
    items = items[:limit]
    return {
        "items": items,
        "total": len(items),
        "next_cursor": None,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


# -- 指标 Tab mock 数据 -------------------------------------------------------
# 为 Console 指标 Tab（issue-006）提供本地可验证的快照 + PromQL 时序数据。
# 覆盖场景：snapshot-loading / partial-null（gpu_container GPU「暂不可用」）/
# chart-empty / chart-error / chart-forbidden / 时间范围切换 / GPU 系列差异。

# 四个固定实例 ID，分别对应 container / gpu_container / vm(running) / vm(stopped)。
INSTANCE_ID_CONTAINER = "00000000-0000-4000-8000-000000000001"
INSTANCE_ID_GPU = "00000000-0000-4000-8000-000000000002"
INSTANCE_ID_VM = "00000000-0000-4000-8000-000000000003"
INSTANCE_ID_VM_STOPPED = "00000000-0000-4000-8000-000000000004"
# sandbox 实例（安全事件 Tab 仅对 sandbox kind 可见）。
INSTANCE_ID_SANDBOX = "00000000-0000-4000-8000-000000000005"


def mock_instances_response(query: dict[str, list[str]]) -> dict[str, Any]:
    """为 listInstances 返回 3 个不同 kind 的实例，并响应 kind 过滤。

    便于 Console 实例列表与指标 Tab 在本地 mock 下验证 kind 差异化指标。
    """
    all_items = [
        _mock_instance_record(INSTANCE_ID_CONTAINER, "container", "demo-container-001"),
        _mock_instance_record(INSTANCE_ID_GPU, "gpu_container", "demo-gpu-001"),
        _mock_instance_record(INSTANCE_ID_VM, "vm", "demo-vm-001"),
        _mock_instance_record(INSTANCE_ID_VM_STOPPED, "vm", "demo-vm-002", state="stopped"),
        _mock_instance_record(INSTANCE_ID_SANDBOX, "sandbox", "demo-sandbox-001"),
    ]
    kind = (query.get("kind") or [""])[0]
    items = [it for it in all_items if not kind or it["kind"] == kind]
    return {
        "items": items,
        "total": len(items),
        "next_cursor": None,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


def _mock_instance_record(instance_id: str, kind: str, name: str, state: str = "running") -> dict[str, Any]:
    """构造单个 InstanceRecord mock。state 默认 running，可传 stopped 等验证 disabled 态。"""
    return {
        "id": instance_id,
        "tenant_id": "tenant-mock",
        "name": name,
        "kind": kind,
        "instance_type": kind,
        "state": state,
        "state_reason": None,
        "state_message": f"mock instance {state}",
        "provider": "kubernetes_rest",
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
        "audit_id": None,
        "resource_refs": [],
        "endpoint": None,
        "node_name": "node-mock",
        "termination_protection": False,
        "created_at": "2026-05-20T00:00:00Z",
        "updated_at": "2026-05-20T00:00:00Z",
    }


def mock_instance_response(instance_id: str) -> dict[str, Any]:
    """为 getInstance 按 instance_id 返回对应 kind 的实例详情。"""
    if instance_id == INSTANCE_ID_GPU:
        return _mock_instance_record(INSTANCE_ID_GPU, "gpu_container", "demo-gpu-001")
    if instance_id == INSTANCE_ID_VM:
        return _mock_instance_record(INSTANCE_ID_VM, "vm", "demo-vm-001")
    if instance_id == INSTANCE_ID_VM_STOPPED:
        return _mock_instance_record(INSTANCE_ID_VM_STOPPED, "vm", "demo-vm-002", state="stopped")
    if instance_id == INSTANCE_ID_SANDBOX:
        return _mock_instance_record(INSTANCE_ID_SANDBOX, "sandbox", "demo-sandbox-001")
    # 默认返回 container 实例
    return _mock_instance_record(INSTANCE_ID_CONTAINER, "container", "demo-container-001")


def mock_instance_metrics_response(instance_id: str) -> dict[str, Any]:
    """为 getInstanceMetrics 按 instance_id 返回差异化快照数据。

    覆盖：
    - container：完整 CPU/内存/网络数据
    - gpu_container：GPU 字段全部 null（验证「暂不可用」不显示 0）
    - vm：完整 CPU/内存/网络数据
    """
    base = {
        "instance_id": instance_id,
        "timestamp": "2026-05-20T00:01:00Z",
        "cpu_utilization_pct": 42.3,
        "memory_used_mb": 1024.0,
        "memory_total_mb": 4096.0,
        "network_rx_bytes": 1048576,
        "network_tx_bytes": 524288,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }
    if instance_id == INSTANCE_ID_GPU:
        # GPU 字段全部 null，验证 partial-null 场景（GPU 卡片显示「暂不可用」）
        base.update({
            "gpu_utilization_pct": None,
            "gpu_memory_used_mb": None,
            "gpu_memory_total_mb": None,
        })
    return base


def mock_observability_query_response(query: dict[str, list[str]]) -> tuple[int, dict[str, Any]]:
    """为 queryObservability 返回 PromQL 时序数据，支持多场景切换。

    场景控制（通过 query 参数）：
    - ?error=1：返回 503 + ErrorResponse，验证 chart-error
    - ?forbidden=1：返回 403 + ErrorResponse，验证 chart-forbidden
    - ?empty=1：返回空 results，验证 chart-empty
    - 默认：根据 PromQL 中的 instance_id（注入到 namespace/pod label）返回
      对应曲线；gpu_container 实例额外返回 GPU 曲线

    返回 (status_code, body)。
    """
    # 错误态
    if query.get("error", [""])[0] == "1":
        return 503, {
            "code": "UNAVAILABLE",
            "message": "mock: PromQL 查询失败（由 ?error=1 触发）",
            "request_id": "mock-obs-err-0001",
        }
    # 无权限态
    if query.get("forbidden", [""])[0] == "1":
        return 403, {
            "code": "FORBIDDEN",
            "message": "mock: 无 observability 读权限（由 ?forbidden=1 触发）",
            "request_id": "mock-obs-forbidden-0001",
        }
    # 空数据态
    if query.get("empty", [""])[0] == "1":
        return 200, {
            "query": query.get("query", [""])[0],
            "result_type": "matrix",
            "results": [],
            "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
        }

    # 默认：根据 PromQL 中的指标名生成 12 个时间点的时间序列
    promql = query.get("query", [""])[0]
    results = _build_timeseries_points(promql)
    return 200, {
        "query": promql,
        "result_type": "matrix",
        "results": results,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


def _build_timeseries_points(promql: str) -> list[dict[str, Any]]:
    """根据 PromQL 中的指标名生成 12 个时间点的时间序列（每 5 分钟一个点）。

    支持的指标（与 promqlTemplates.ts 冻结模板对齐）：
    - container_cpu_usage_seconds_total → CPU 利用率（30-70 波动）
    - container_memory_working_set_bytes → 内存使用率（40-80 波动）
    - DCGM_FI_DEV_GPU_UTIL → GPU 利用率（50-90 波动）
    - DCGM_FI_DEV_FB_USED → GPU 显存使用率（30-60 波动）
    """
    base_time = datetime.datetime(2026, 5, 20, 0, 0, 0)
    points: list[dict[str, Any]] = []
    for i in range(12):
        ts = (base_time + datetime.timedelta(minutes=5 * i)).strftime("%Y-%m-%dT%H:%M:%SZ")
        if "container_cpu_usage_seconds_total" in promql:
            value = 30 + 5 * i + (i % 3) * 2  # 30..70
        elif "container_memory_working_set_bytes" in promql:
            value = 40 + 4 * i + (i % 2) * 3  # 40..80
        elif "DCGM_FI_DEV_GPU_UTIL" in promql:
            value = 50 + 4 * i  # 50..94
        elif "DCGM_FI_DEV_FB_USED" in promql:
            value = 30 + 3 * i  # 30..63
        else:
            value = 50.0
        points.append({
            "metric": {"__name__": promql[:32]},
            "value": float(value),
            "timestamp": ts,
        })
    return points


def mock_create_exec_session_response(
    instance_id: str, query: dict[str, list[str]]
) -> tuple[int, dict[str, Any]]:
    """为 createInstanceExecSession 返回 exec session mock，支持场景切换。

    覆盖：
    - ?forbidden=1：返回 403，验证 TerminalTab「无 exec 权限」Alert warning
    - ?error=1：返回 422 + ErrorResponse，验证 exec 4xx/422 失败 → Message.error + idle
    - ?expired=1：返回 expires_at 已过期的 session，验证「会话已过期」Alert + 重新连接
    - 默认：返回有效 ws_url（指向本地 echo websocket）+ expires_at 未来 1 小时

    返回 (status_code, body)。
    """
    # 无 exec 权限：403
    if query.get("forbidden", [""])[0] == "1":
        return 403, {
            "code": "FORBIDDEN",
            "message": "mock: 无 scope:instances:exec 权限（由 ?forbidden=1 触发）",
            "request_id": "mock-exec-forbidden-0001",
        }
    # 4xx/422 失败：Message.error + 保留 idle
    if query.get("error", [""])[0] == "1":
        return 422, {
            "code": "INVALID_ARGUMENT",
            "message": "mock: exec 请求参数无效（由 ?error=1 触发）",
            "request_id": "mock-exec-err-0001",
        }
    # 生成 session id
    session_id = "00000000-0000-4000-8000-" + instance_id.replace("-", "")[-12:].rjust(12, "0")
    # ws_url 指向本地 echo websocket（由 mock 同进程在单独 WS 端口上提供）
    ws_url = f"ws://127.0.0.1:4011/ws/exec/{session_id}"
    # expires_at：默认未来 1 小时；?expired=1 设为过去时间
    if query.get("expired", [""])[0] == "1":
        expires_at = "2020-01-01T00:00:00Z"
    else:
        expires_at = (datetime.datetime.utcnow() + datetime.timedelta(hours=1)).strftime(
            "%Y-%m-%dT%H:%M:%SZ"
        )
    return 200, {
        "id": session_id,
        "instance_id": instance_id,
        "ws_url": ws_url,
        "token": "mock-exec-token-" + session_id[-12:],
        "expires_at": expires_at,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


def mock_create_console_session_response(
    instance_id: str, query: dict[str, list[str]]
) -> tuple[int, dict[str, Any]]:
    """为 createInstanceConsoleSession 返回 console/VNC session mock，支持场景切换。

    覆盖：
    - ?forbidden=1：返回 403，验证 ConsoleTab「无控制台权限」Alert warning
    - ?error=1：返回 422 + ErrorResponse，验证 ConsoleTab 失败 → Message.error + idle
    - 默认：返回有效 connect_url + expires_at 未来 1 小时，验证 opened 态

    返回 (status_code, body)。
    """
    # 无 console 权限：403
    if query.get("forbidden", [""])[0] == "1":
        return 403, {
            "code": "FORBIDDEN",
            "message": "mock: 无 scope:instances:console 权限（由 ?forbidden=1 触发）",
            "request_id": "mock-console-forbidden-0001",
        }
    # 4xx/422 失败：Message.error + 保留 idle
    if query.get("error", [""])[0] == "1":
        return 422, {
            "code": "INVALID_ARGUMENT",
            "message": "mock: console 请求参数无效（由 ?error=1 触发）",
            "request_id": "mock-console-err-0001",
        }
    # 生成 session id
    session_id = "00000000-0000-4000-8000-" + instance_id.replace("-", "")[-12:].rjust(12, "0")
    # connect_url 指向一个占位页面（验证 window.open 行为）
    connect_url = f"http://127.0.0.1:4010/api/v1/mock/console/{session_id}"
    url = connect_url
    # expires_at：默认未来 1 小时
    expires_at = (datetime.datetime.utcnow() + datetime.timedelta(hours=1)).strftime(
        "%Y-%m-%dT%H:%M:%SZ"
    )
    return 200, {
        "session_id": session_id,
        "protocol": "vnc",
        "connect_url": connect_url,
        "url": url,
        "expires_at": expires_at,
        "dev_profile": {"mode": "local", "provider": "mock", "real_provider": True, "reason": "mock"},
    }


# -- 终端 exec WebSocket echo 服务器（标准库实现，无外部依赖）--------------
# 为 TerminalTab 提供 ws_url 的真实回显服务，验证 xterm.js 渲染与帧契约（SPEC §5.3.2）。
# 行为：
# - 握手：HTTP Upgrade + Sec-WebSocket-Accept（RFC 6455）
# - 收到 stdin 帧 → 回显一个 stdout 帧（带 "echo: " 前缀），让用户看到输入
# - 收到 resize 帧 → 忽略（仅记录）
# - 连接建立时发送一条欢迎 stdout 帧
# 用标准库 socket + 手写帧编解码，避免引入 websockets 依赖。

WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


def _ws_accept_key(client_key: str) -> str:
    """计算 Sec-WebSocket-Accept（RFC 6455 §1.3）。"""
    digest = hashlib.sha1((client_key + WS_GUID).encode("ascii")).digest()
    return base64.b64encode(digest).decode("ascii")


def _ws_read_frame(conn: socket.socket) -> tuple[int, bytes]:
    """读取一个 WebSocket 帧，返回 (opcode, payload)。

    仅处理客户端帧（opcode 0x1 文本 / 0x8 关闭 / 0x9 ping）。忽略掩码。
    """
    header = conn.recv(2)
    if len(header) < 2:
        return 0, b""
    opcode = header[0] & 0x0F
    masked = (header[1] & 0x80) != 0
    length = header[1] & 0x7F
    if length == 126:
        length = struct.unpack(">H", conn.recv(2))[0]
    elif length == 127:
        length = struct.unpack(">Q", conn.recv(8))[0]
    mask = b""
    if masked:
        mask = conn.recv(4)
    payload = b""
    while len(payload) < length:
        chunk = conn.recv(length - len(payload))
        if not chunk:
            break
        payload += chunk
    if masked and mask:
        payload = bytes(payload[i] ^ mask[i % 4] for i in range(len(payload)))
    return opcode, payload


def _ws_send_frame(conn: socket.socket, opcode: int, payload: bytes) -> None:
    """发送一个 WebSocket 帧到客户端（服务端帧不掩码）。"""
    header = bytearray()
    header.append(0x80 | (opcode & 0x0F))
    length = len(payload)
    if length < 126:
        header.append(length)
    elif length < 65536:
        header.append(126)
        header.extend(struct.pack(">H", length))
    else:
        header.append(127)
        header.extend(struct.pack(">Q", length))
    conn.sendall(bytes(header) + payload)


def _ws_handle_client(conn: socket.socket, addr: tuple[str, int]) -> None:
    """处理单个 WebSocket 客户端：握手 + echo 循环。"""
    try:
        # 1. HTTP Upgrade 握手
        request = b""
        while b"\r\n\r\n" not in request:
            chunk = conn.recv(4096)
            if not chunk:
                return
            request += chunk
        request_text = request.decode("ascii", errors="replace")
        key_match = re.search(r"Sec-WebSocket-Key:\s*([A-Za-z0-9+/=]+)", request_text, re.IGNORECASE)
        if not key_match:
            conn.sendall(b"HTTP/1.1 400 Bad Request\r\n\r\n")
            return
        accept = _ws_accept_key(key_match.group(1))
        response = (
            "HTTP/1.1 101 Switching Protocols\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Accept: {accept}\r\n"
            "\r\n"
        )
        conn.sendall(response.encode("ascii"))

        # 2. 连接建立后发送欢迎帧（stdout），对齐 SPEC §5.3.2
        welcome = json.dumps({"type": "stdout", "data": "已连接到 mock 终端（echo 模式）。输入内容将被回显。\r\n"})
        _ws_send_frame(conn, 0x01, welcome.encode("utf-8"))

        # 3. echo 循环
        while True:
            opcode, payload = _ws_read_frame(conn)
            if opcode == 0x8:  # close
                _ws_send_frame(conn, 0x08, b"")
                return
            if opcode == 0x9:  # ping
                _ws_send_frame(conn, 0x0A, payload)
                continue
            if opcode != 0x1:  # 仅处理文本帧
                continue
            try:
                frame = json.loads(payload.decode("utf-8"))
            except (json.JSONDecodeError, UnicodeDecodeError):
                continue
            ftype = frame.get("type")
            if ftype == "stdin":
                # 回显 stdin 为 stdout，前缀 "echo: "
                echo = json.dumps({"type": "stdout", "data": "echo: " + frame.get("data", "")})
                _ws_send_frame(conn, 0x01, echo.encode("utf-8"))
            elif ftype == "resize":
                # 忽略 resize 帧（mock 不模拟真实终端尺寸变化）
                pass
    except (ConnectionError, OSError):
        pass
    finally:
        try:
            conn.close()
        except OSError:
            pass


def start_ws_echo_server(host: str, port: int) -> None:
    """启动 WebSocket echo 服务器（阻塞，应在后台线程中运行）。"""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind((host, port))
    sock.listen(16)
    while True:
        conn, addr = sock.accept()
        threading.Thread(target=_ws_handle_client, args=(conn, addr), daemon=True).start()


def make_handler(routes: list[MockRoute], base_path: str) -> type[BaseHTTPRequestHandler]:
    class CoreMockHandler(BaseHTTPRequestHandler):
        def do_OPTIONS(self) -> None:
            self.send_response(204)
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-API-Key")
            self.send_header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
            self.end_headers()

        def do_GET(self) -> None:
            self.respond()

        def do_POST(self) -> None:
            self.respond()

        def do_PUT(self) -> None:
            self.respond()

        def do_PATCH(self) -> None:
            self.respond()

        def do_DELETE(self) -> None:
            self.respond()

        def log_message(self, fmt: str, *args: Any) -> None:
            return

        def respond(self) -> None:
            parsed = urlparse(self.path)
            path = parsed.path
            route = find_route(routes, self.command, path, base_path)
            if route is None:
                self.send_json(404, {"code": "NOT_FOUND", "message": "mock route not found", "request_id": "mock"})
                return
            if route.status_code == 204:
                self.send_response(204)
                self.send_header("X-ANI-Mock-Operation", route.operation_id)
                self.end_headers()
                return
            # 对 listInstanceLogs 注入多条不同 level 的日志并响应 level 过滤，
            # 便于 Console 日志 Tab 级别筛选用例在本地 mock 下可验证。
            body = route.body
            if route.operation_id == "listInstanceLogs":
                body = mock_logs_response(parse_qs(parsed.query))
            # 对 listInstanceEvents 注入多条 Normal/Warning 事件并响应 type 过滤，
            # 便于 Console 事件 Tab 类型筛选、空态、loading 用例在本地 mock 下可验证。
            # 手动测 error 态：在请求 URL 加 ?error=1 即返回 503 + ErrorResponse，
            # 用于验证 EventsTab 的 Alert theme="error" + message + request_id + 重试。
            # 例如：/api/v1/instances/{id}/events?error=1
            if route.operation_id == "listInstanceEvents":
                query = parse_qs(parsed.query)
                if query.get("error", [""])[0] == "1":
                    self.send_json(
                        503,
                        {
                            "code": "UNAVAILABLE",
                            "message": "mock: 事件服务暂时不可用（由 ?error=1 触发）",
                            "request_id": "mock-evt-err-0001",
                        },
                        "application/json",
                        route.operation_id,
                    )
                    return
                body = mock_events_response(query)
            # 对 listInstanceSecurityEvents 注入多条不同 severity 的安全事件并响应 severity 过滤，
            # 便于 Console 安全事件 Tab（issue-009）severity 筛选、空态、loading 用例在本地 mock 下可验证。
            # 手动测 error 态：在请求 URL 加 ?error=1 即返回 503 + ErrorResponse，
            # 用于验证 SecurityEventsTab 的 Alert theme="error" + message + request_id + 重试。
            # 例如：/api/v1/instances/{id}/security-events?error=1
            if route.operation_id == "listInstanceSecurityEvents":
                query = parse_qs(parsed.query)
                if query.get("error", [""])[0] == "1":
                    self.send_json(
                        503,
                        {
                            "code": "UNAVAILABLE",
                            "message": "mock: 安全事件服务暂时不可用（由 ?error=1 触发）",
                            "request_id": "mock-sev-err-0001",
                        },
                        "application/json",
                        route.operation_id,
                    )
                    return
                body = mock_security_events_response(query)
            # -- 指标 Tab mock 数据分发（issue-006）---------------------------------
            # listInstances：返回 3 个不同 kind 的实例，响应 kind 过滤
            if route.operation_id == "listInstances":
                body = mock_instances_response(parse_qs(parsed.query))
            # getInstance：按 path 中的 instance_id 返回对应 kind 实例详情
            if route.operation_id == "getInstance":
                instance_id = _extract_instance_id(path, base_path)
                body = mock_instance_response(instance_id)
            # getInstanceMetrics：按 instance_id 返回差异化快照数据
            # 手动测 error 态：?error=1 返回 503，验证 snapshot-error
            if route.operation_id == "getInstanceMetrics":
                query = parse_qs(parsed.query)
                if query.get("error", [""])[0] == "1":
                    self.send_json(
                        503,
                        {
                            "code": "UNAVAILABLE",
                            "message": "mock: 指标快照服务暂时不可用（由 ?error=1 触发）",
                            "request_id": "mock-metrics-err-0001",
                        },
                        "application/json",
                        route.operation_id,
                    )
                    return
                instance_id = _extract_instance_id(path, base_path)
                body = mock_instance_metrics_response(instance_id)
            # queryObservability：PromQL 代理查询，支持 error/forbidden/empty 场景切换
            if route.operation_id == "queryObservability":
                status, obs_body = mock_observability_query_response(parse_qs(parsed.query))
                self.send_json(status, obs_body, "application/json", route.operation_id)
                return
            # createInstanceExecSession：终端 exec session，支持 forbidden/error/expired 场景切换
            if route.operation_id == "createInstanceExecSession":
                instance_id = _extract_instance_id(path, base_path)
                status, exec_body = mock_create_exec_session_response(
                    instance_id, parse_qs(parsed.query)
                )
                self.send_json(status, exec_body, "application/json", route.operation_id)
                return
            # createInstanceConsoleSession：VM console/VNC session，支持 forbidden/error 场景切换
            # 手动测 error 态：在请求 URL 加 ?error=1 即返回 422 + ErrorResponse，验证 ConsoleTab Message.error
            # 手动测 forbidden 态：?forbidden=1 返回 403，验证 ConsoleTab「无控制台权限」Alert warning
            if route.operation_id == "createInstanceConsoleSession":
                instance_id = _extract_instance_id(path, base_path)
                status, console_body = mock_create_console_session_response(
                    instance_id, parse_qs(parsed.query)
                )
                self.send_json(status, console_body, "application/json", route.operation_id)
                return
            self.send_json(route.status_code, body, route.content_type, route.operation_id)

        def send_json(
            self,
            status_code: int,
            body: Any,
            content_type: str = "application/json",
            operation_id: str = "",
        ) -> None:
            payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
            self.send_response(status_code)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Access-Control-Allow-Origin", "*")
            if operation_id:
                self.send_header("X-ANI-Mock-Operation", operation_id)
            self.end_headers()
            self.wfile.write(payload)

    return CoreMockHandler


def main() -> None:
    parser = argparse.ArgumentParser(description="Serve ANI Core API mock from api/openapi/v1.yaml")
    parser.add_argument("--spec", default=str(DEFAULT_SPEC), help="OpenAPI contract path")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=4010)
    parser.add_argument(
        "--ws-port",
        type=int,
        default=4011,
        help="WebSocket echo server port for exec terminal mock (default 4011)",
    )
    parser.add_argument(
        "--no-ws",
        action="store_true",
        help="Disable the WebSocket echo server (only HTTP mock)",
    )
    args = parser.parse_args()

    spec = load_spec(Path(args.spec))
    base_path = server_base_path(spec)
    routes = build_routes(spec)

    # 启动 WebSocket echo 服务器（后台线程），为 TerminalTab exec 提供 ws_url 回显
    if not args.no_ws:
        ws_thread = threading.Thread(
            target=start_ws_echo_server,
            args=(args.host, args.ws_port),
            daemon=True,
        )
        ws_thread.start()
        print(
            f"WebSocket echo server listening on ws://{args.host}:{args.ws_port} (exec terminal mock)"
        )

    server = HTTPServer((args.host, args.port), make_handler(routes, base_path))
    print(f"ANI Core mock server listening on http://{args.host}:{args.port}{base_path} ({len(routes)} routes)")
    server.serve_forever()


if __name__ == "__main__":
    main()
