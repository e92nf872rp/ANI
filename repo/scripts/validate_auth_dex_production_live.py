#!/usr/bin/env python3
"""Run Sprint 13 Auth/Dex production-shaped live gate and write sanitized evidence."""

from __future__ import annotations

import argparse
import base64
import html.parser
import json
import time
import urllib.error
import urllib.parse
import urllib.request
from http.cookiejar import CookieJar
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/sprint13-auth-dex-production-evidence.json"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: N802
        return None


class LoginFormParser(html.parser.HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.form_action = ""
        self.inputs: dict[str, str] = {}
        self._in_form = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = {name: value or "" for name, value in attrs}
        if tag == "form" and not self.form_action:
            self._in_form = True
            self.form_action = values.get("action", "")
            return
        if self._in_form and tag == "input":
            name = values.get("name", "")
            if name:
                self.inputs[name] = values.get("value", "")

    def handle_endtag(self, tag: str) -> None:
        if tag == "form" and self._in_form:
            self._in_form = False


def fail(message: str) -> None:
    raise SystemExit(f"auth dex production live gate invalid: {message}")


def request(
    opener: urllib.request.OpenerDirector,
    method: str,
    url: str,
    *,
    data: dict[str, Any] | None = None,
    headers: dict[str, str] | None = None,
    json_body: dict[str, Any] | None = None,
) -> tuple[int, dict[str, str], bytes]:
    encoded = None
    request_headers = dict(headers or {})
    if json_body is not None:
        encoded = json.dumps(json_body).encode("utf-8")
        request_headers["Content-Type"] = "application/json"
    elif data is not None:
        encoded = urllib.parse.urlencode(data).encode("utf-8")
        request_headers["Content-Type"] = "application/x-www-form-urlencoded"
    req = urllib.request.Request(url, data=encoded, method=method, headers=request_headers)
    try:
        with opener.open(req, timeout=20) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as err:
        return err.code, dict(err.headers), err.read()
    except urllib.error.URLError as err:
        fail(f"request {url} failed: {err}")


def parse_json(body: bytes, label: str) -> dict[str, Any]:
    try:
        payload = json.loads(body.decode("utf-8"))
    except json.JSONDecodeError as err:
        fail(f"{label} did not return JSON: {err}")
    if not isinstance(payload, dict):
        fail(f"{label} must return a JSON object")
    return payload


def wait_for_discovery(
    opener: urllib.request.OpenerDirector,
    discovery_issuer: str,
    expected_issuer: str,
    timeout_seconds: int,
) -> dict[str, Any]:
    discovery_url = discovery_issuer.rstrip("/") + "/.well-known/openid-configuration"
    deadline = time.time() + timeout_seconds
    last_status = ""
    while time.time() < deadline:
        status, _, body = request(opener, "GET", discovery_url)
        if status == 200:
            payload = parse_json(body, "Dex discovery")
            if payload.get("issuer") != expected_issuer.rstrip("/"):
                fail("Dex discovery issuer mismatch")
            return payload
        last_status = f"HTTP {status}"
        time.sleep(1)
    fail(f"Dex discovery did not become ready: {last_status}")


def parse_login_form(body: bytes, base_url: str) -> tuple[str, dict[str, str]]:
    parser = LoginFormParser()
    parser.feed(body.decode(errors="replace"))
    if not parser.form_action:
        fail("Dex login form action not found")
    return urllib.parse.urljoin(base_url, parser.form_action), parser.inputs


def get_login_page(opener: urllib.request.OpenerDirector, url: str, redirect_prefix: str) -> tuple[str, bytes]:
    current = url
    for _ in range(8):
        status, headers, body = request(opener, "GET", current)
        if status == 200:
            return current, body
        location = headers.get("Location", "")
        if status not in (301, 302, 303, 307, 308) or not location:
            fail(f"expected login page or redirect from Dex, got HTTP {status}")
        next_url = urllib.parse.urljoin(current, location)
        if next_url.startswith(redirect_prefix):
            fail("Dex redirected to callback before login")
        current = next_url
    fail("too many redirects before Dex login page")


def follow_until_callback(opener: urllib.request.OpenerDirector, url: str, redirect_prefix: str) -> str:
    current = url
    for _ in range(8):
        if current.startswith(redirect_prefix):
            return current
        status, headers, _ = request(opener, "GET", current)
        location = headers.get("Location", "")
        if status not in (301, 302, 303, 307, 308) or not location:
            fail(f"expected Dex redirect, got HTTP {status}")
        current = urllib.parse.urljoin(current, location)
    fail("too many redirects before callback")


def rewrite_internal_issuer_url(url: str, *, expected_issuer: str, public_issuer: str) -> str:
    if not public_issuer.strip():
        return url
    expected = expected_issuer.rstrip("/")
    if not url.startswith(expected):
        return url
    public = public_issuer.rstrip("/")
    return public + url[len(expected):]


def bearer_headers(token: str) -> dict[str, str]:
    return {"Authorization": "Bearer " + token}


def resolve_password(args: argparse.Namespace) -> str:
    if args.password_file:
        try:
            password = Path(args.password_file).read_text(encoding="utf-8").strip()
        except OSError as exc:
            fail(f"unable to read password file: {exc}")
    else:
        password = args.password.strip()
    if not password:
        fail("--password or --password-file is required")
    return password


def run(args: argparse.Namespace) -> dict[str, Any]:
    gateway = args.gateway_url.rstrip("/")
    dex_issuer = args.dex_issuer.rstrip("/")
    dex_transport_issuer = (args.dex_public_issuer or args.dex_issuer).rstrip("/")
    redirect_uri = args.redirect_uri
    password = resolve_password(args)
    cookies = CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies), NoRedirect())

    discovery = wait_for_discovery(opener, dex_transport_issuer, dex_issuer, args.timeout_seconds)
    jwks_uri = rewrite_internal_issuer_url(
        str(discovery["jwks_uri"]),
        expected_issuer=dex_issuer,
        public_issuer=args.dex_public_issuer,
    )
    jwks_status, _, jwks_body = request(opener, "GET", jwks_uri)
    jwks = parse_json(jwks_body, "Dex JWKS") if jwks_status == 200 else {}
    if jwks_status != 200 or not jwks.get("keys"):
        fail("Dex JWKS did not contain signing keys")

    anonymous_status, _, _ = request(opener, "GET", gateway + args.protected_path)
    if anonymous_status != 401:
        fail(f"protected route anonymous status = {anonymous_status}, want 401")

    oidc_begin_status, _, begin_body = request(
        opener,
        "POST",
        gateway + "/api/v1/auth/oidc/begin",
        json_body={"tenant_name": args.tenant_name, "redirect_uri": redirect_uri},
    )
    if oidc_begin_status != 200:
        fail(f"OIDC begin returned HTTP {oidc_begin_status}: {begin_body.decode(errors='replace')}")
    begin = parse_json(begin_body, "OIDC begin")
    auth_url = rewrite_internal_issuer_url(
        str(begin.get("authorization_url", "")),
        expected_issuer=dex_issuer,
        public_issuer=args.dex_public_issuer,
    )
    state = str(begin.get("state", ""))
    if not auth_url or not state:
        fail("OIDC begin response missing authorization_url or state")

    login_page_url, login_body = get_login_page(opener, auth_url, redirect_uri)
    login_url, form = parse_login_form(login_body, login_page_url)
    form.update({"login": args.username, "password": password})
    status, headers, _ = request(opener, "POST", login_url, data=form)
    if status not in (301, 302, 303):
        fail(f"Dex login returned HTTP {status}")
    callback_url = follow_until_callback(opener, urllib.parse.urljoin(login_url, headers["Location"]), redirect_uri)
    callback = urllib.parse.urlparse(callback_url)
    params = urllib.parse.parse_qs(callback.query)
    if params.get("state", [""])[0] != state:
        fail("OIDC callback state mismatch")
    code = params.get("code", [""])[0]
    if not code:
        fail("OIDC callback missing code")

    oidc_complete_status, _, complete_body = request(
        opener,
        "POST",
        gateway + "/api/v1/auth/token",
        json_body={"state": state, "code": code, "redirect_uri": redirect_uri},
    )
    if oidc_complete_status != 200:
        fail(f"OIDC complete returned HTTP {oidc_complete_status}: {complete_body.decode(errors='replace')}")
    token_pair = parse_json(complete_body, "OIDC complete")
    access_token = str(token_pair.get("access_token", ""))
    refresh_token = str(token_pair.get("refresh_token", ""))
    if not access_token or not refresh_token:
        fail("OIDC complete response missing token pair")

    authorized_status, _, _ = request(opener, "GET", gateway + args.protected_path, headers=bearer_headers(access_token))
    if authorized_status != 200:
        fail(f"protected route authorized status = {authorized_status}, want 200")

    refresh_status, _, refresh_body = request(
        opener,
        "POST",
        gateway + "/api/v1/auth/refresh",
        json_body={"refresh_token": refresh_token},
    )
    if refresh_status != 200:
        fail(f"refresh returned HTTP {refresh_status}: {refresh_body.decode(errors='replace')}")

    return {
        "status": "passed",
        "auth_dex_production_shape": {
            "status": "passed",
            "gateway_auth_mode": "auth_service",
            "proof_items": [
                "gateway_non_dev_auth",
                "dex_discovery_and_jwks",
                "gateway_rejects_anonymous",
                "gateway_accepts_dex_oidc_token",
                "gateway_refresh_token",
                "auth_service_rbac_check",
            ],
            "anonymous_status": anonymous_status,
            "oidc_begin_status": oidc_begin_status,
            "oidc_complete_status": oidc_complete_status,
            "authorized_status": authorized_status,
            "refresh_status": refresh_status,
            "dex_issuer": dex_issuer,
            "dex_transport_issuer": dex_transport_issuer,
            "protected_path": args.protected_path,
        },
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--dex-issuer", required=True)
    parser.add_argument("--dex-public-issuer", default="", help="optional externally reachable Dex issuer URL for browser-style login")
    parser.add_argument("--tenant-name", required=True)
    parser.add_argument("--username", required=True)
    parser.add_argument("--password", default="")
    parser.add_argument("--password-file", default="", help="file containing the Dex test password; preferred over --password")
    parser.add_argument("--redirect-uri", default="http://ani-gateway.ani-system.svc.cluster.local:8080/auth/callback")
    parser.add_argument("--protected-path", default="/api/v1/auth/api-keys")
    parser.add_argument("--timeout-seconds", type=int, default=60)
    parser.add_argument("--evidence-output", default=str(DEFAULT_EVIDENCE))
    args = parser.parse_args()

    evidence = run(args)
    output = Path(args.evidence_output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"Auth/Dex production live gate passed; evidence written to {output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
