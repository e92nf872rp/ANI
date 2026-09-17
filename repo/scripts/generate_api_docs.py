#!/usr/bin/env python3
"""Generate static API documentation from ANI OpenAPI contracts."""

from __future__ import annotations

from html import escape
from pathlib import Path
from typing import Any
import re

import yaml


ROOT = Path(__file__).resolve().parents[1]
OUTPUT_DIR = ROOT / "docs/api"
SPECS = {
    "core": ROOT / "api/openapi/v1.yaml",
    "services": ROOT / "api/openapi/services/v1.yaml",
}
HTTP_METHODS = {"get", "post", "put", "patch", "delete"}


def load_spec(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        raise ValueError(f"{path} must parse to an object")
    return data


def collect_operations(spec: dict[str, Any]) -> list[dict[str, str]]:
    operations: list[dict[str, str]] = []
    for path, path_item in sorted(spec.get("paths", {}).items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            operations.append(
                {
                    "method": method.upper(),
                    "path": path,
                    "operation_id": operation.get("operationId") or fallback_operation_id(method, path),
                    "summary": operation.get("summary", ""),
                    "scope": operation.get("x-ani-rbac-scope", ""),
                    "responses": ", ".join(sorted(str(code) for code in operation.get("responses", {}).keys())),
                }
            )
    return operations


def fallback_operation_id(method: str, path: str) -> str:
    tokens = re.sub(r"[^a-zA-Z0-9]+", " ", path).title().replace(" ", "")
    return method.lower() + tokens


def schema_names(spec: dict[str, Any]) -> list[str]:
    schemas = spec.get("components", {}).get("schemas", {})
    return sorted(schemas.keys())


def render_layer(layer: str, spec: dict[str, Any]) -> str:
    info = spec.get("info", {})
    operations = collect_operations(spec)
    schemas = schema_names(spec)
    server_url = (spec.get("servers") or [{}])[0].get("url", "")
    title = f"ANI {layer.title()} API"
    operation_rows = "\n".join(
        f"""        <tr>
          <td><span class="method method-{escape(item['method'].lower())}">{escape(item['method'])}</span></td>
          <td><code>{escape(item['path'])}</code></td>
          <td><code>{escape(item['operation_id'])}</code></td>
          <td>{escape(item['summary'])}</td>
          <td><code>{escape(item['scope'])}</code></td>
          <td>{escape(item['responses'])}</td>
        </tr>"""
        for item in operations
    )
    schema_items = "\n".join(f"        <li><code>{escape(name)}</code></li>" for name in schemas)
    return page_shell(
        title=title,
        body=f"""
      <section class="hero">
        <p class="eyebrow">Generated from {escape(relative_spec(layer))}</p>
        <h1>{escape(info.get('title', title))}</h1>
        <p>{escape(first_paragraph(info.get('description', '')))}</p>
        <dl class="facts">
          <div><dt>Version</dt><dd>{escape(info.get('version', ''))}</dd></div>
          <div><dt>Server</dt><dd><code>{escape(server_url)}</code></dd></div>
          <div><dt>Operations</dt><dd>{len(operations)}</dd></div>
          <div><dt>Schemas</dt><dd>{len(schemas)}</dd></div>
        </dl>
      </section>

      <section>
        <h2>Operations</h2>
        <table>
          <thead>
            <tr>
              <th>Method</th>
              <th>Path</th>
              <th>Operation</th>
              <th>Summary</th>
              <th>RBAC Scope</th>
              <th>Responses</th>
            </tr>
          </thead>
          <tbody>
{operation_rows}
          </tbody>
        </table>
      </section>

      <section>
        <h2>Schemas</h2>
        <ul class="schema-list">
{schema_items}
        </ul>
      </section>
""",
    )


def render_index(core_spec: dict[str, Any], services_spec: dict[str, Any]) -> str:
    return page_shell(
        title="ANI API Documentation",
        body=f"""
      <section class="hero">
        <p class="eyebrow">DOC-API-A</p>
        <h1>ANI API Documentation</h1>
        <p>Static API docs generated from the Core and Services API contracts. Core remains infrastructure-only; Services owns business APIs.</p>
        <dl class="facts">
          <div><dt>Core Operations</dt><dd>{len(collect_operations(core_spec))}</dd></div>
          <div><dt>Services Operations</dt><dd>{len(collect_operations(services_spec))}</dd></div>
          <div><dt>Core Spec</dt><dd><code>api/openapi/v1.yaml</code></dd></div>
          <div><dt>Services Spec</dt><dd><code>api/openapi/services/v1.yaml</code></dd></div>
        </dl>
      </section>

      <section class="cards">
        <a class="doc-card" href="core.html">
          <span>Core API</span>
          <strong>Infrastructure platform API</strong>
          <small>Instances, networks, storage, auth, operations, metering.</small>
        </a>
        <a class="doc-card" href="services.html">
          <span>Services API</span>
          <strong>Cloud services API</strong>
          <small>Models, inference services, knowledge bases.</small>
        </a>
      </section>
""",
    )


def page_shell(title: str, body: str) -> str:
    return f"""<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{escape(title)}</title>
    <style>
      :root {{
        color-scheme: light;
        --ink: #17202a;
        --muted: #667085;
        --line: #d9dee8;
        --bg: #f7f9fc;
        --panel: #ffffff;
        --blue: #1b5faa;
        --green: #167c58;
        --orange: #9a5b00;
        --red: #b42318;
      }}
      * {{ box-sizing: border-box; }}
      body {{
        margin: 0;
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        color: var(--ink);
        background: var(--bg);
      }}
      main {{
        width: min(1180px, calc(100vw - 32px));
        margin: 0 auto;
        padding: 32px 0 56px;
      }}
      .hero {{
        background: var(--panel);
        border: 1px solid var(--line);
        border-radius: 8px;
        padding: 24px;
      }}
      .eyebrow {{
        margin: 0 0 8px;
        color: var(--blue);
        font-size: 13px;
        font-weight: 700;
        text-transform: uppercase;
      }}
      h1 {{ margin: 0 0 12px; font-size: 32px; line-height: 1.15; }}
      h2 {{ margin: 28px 0 12px; font-size: 20px; }}
      p {{ color: var(--muted); line-height: 1.6; }}
      code {{
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 13px;
      }}
      .facts {{
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
        gap: 12px;
        margin: 22px 0 0;
      }}
      .facts div {{
        border: 1px solid var(--line);
        border-radius: 8px;
        padding: 12px;
        background: #fbfcff;
      }}
      dt {{ color: var(--muted); font-size: 12px; }}
      dd {{ margin: 4px 0 0; font-weight: 700; }}
      table {{
        width: 100%;
        border-collapse: collapse;
        background: var(--panel);
        border: 1px solid var(--line);
        border-radius: 8px;
        overflow: hidden;
      }}
      th, td {{
        padding: 10px 12px;
        border-bottom: 1px solid var(--line);
        text-align: left;
        vertical-align: top;
        font-size: 14px;
      }}
      th {{ color: var(--muted); background: #eef3f8; font-size: 12px; text-transform: uppercase; }}
      tr:last-child td {{ border-bottom: 0; }}
      .method {{
        display: inline-block;
        min-width: 58px;
        border-radius: 6px;
        padding: 4px 8px;
        color: #fff;
        text-align: center;
        font-size: 12px;
        font-weight: 800;
      }}
      .method-get {{ background: var(--blue); }}
      .method-post {{ background: var(--green); }}
      .method-put, .method-patch {{ background: var(--orange); }}
      .method-delete {{ background: var(--red); }}
      .schema-list {{
        columns: 2;
        background: var(--panel);
        border: 1px solid var(--line);
        border-radius: 8px;
        padding: 18px 28px;
      }}
      .schema-list li {{ margin: 6px 0; }}
      .cards {{
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
        gap: 16px;
        margin-top: 20px;
      }}
      .doc-card {{
        display: block;
        min-height: 150px;
        border: 1px solid var(--line);
        border-radius: 8px;
        padding: 18px;
        background: var(--panel);
        color: inherit;
        text-decoration: none;
      }}
      .doc-card span {{ color: var(--blue); font-size: 13px; font-weight: 800; }}
      .doc-card strong {{ display: block; margin: 10px 0; font-size: 21px; }}
      .doc-card small {{ color: var(--muted); line-height: 1.5; }}
      @media (max-width: 760px) {{
        main {{ width: min(100vw - 20px, 1180px); padding-top: 18px; }}
        .hero {{ padding: 18px; }}
        h1 {{ font-size: 26px; }}
        table {{ display: block; overflow-x: auto; }}
        .schema-list {{ columns: 1; }}
      }}
    </style>
  </head>
  <body>
    <main>
{body}
    </main>
  </body>
</html>
"""


def first_paragraph(value: str) -> str:
    stripped = value.strip()
    if not stripped:
        return ""
    return stripped.split("\n\n", 1)[0].replace("\n", " ")


def relative_spec(layer: str) -> str:
    return "api/openapi/v1.yaml" if layer == "core" else "api/openapi/services/v1.yaml"


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def main() -> None:
    specs = {layer: load_spec(path) for layer, path in SPECS.items()}
    write(OUTPUT_DIR / "index.html", render_index(specs["core"], specs["services"]))
    for layer, spec in specs.items():
        write(OUTPUT_DIR / f"{layer}.html", render_layer(layer, spec))
    print(f"API docs generated at {OUTPUT_DIR.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
