"""Orva Python SDK — kv, invoke, jobs.

Available inside any function running on Orva. Routes through the
ORVA_API_BASE loopback URL using the per-process ORVA_INTERNAL_TOKEN
that the worker received at spawn time. Both env vars are present in
production and absent in tests; helpers raise OrvaUnavailableError when
the SDK can't reach the host.

Usage:

    from orva import kv, invoke, jobs

    kv.put("count", 42, ttl_seconds=60)
    n = kv.get("count", default=0)

    result = invoke("resize-image", {"url": "..."})

    jobs.enqueue("send-welcome-email", {"to": "ada@example.com"})
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request


class OrvaError(RuntimeError):
    """Base class for SDK errors. Status codes from the upstream HTTP
    response surface as `status` so callers can branch."""

    def __init__(self, message: str, status: int = 0):
        super().__init__(message)
        self.status = status


class OrvaUnavailableError(OrvaError):
    """ORVA_API_BASE / ORVA_INTERNAL_TOKEN are missing — the SDK can't
    reach the host. Typically only happens in tests or when running the
    handler outside of Orva's sandbox."""


def _api_base() -> str:
    return os.environ.get("ORVA_API_BASE", "")


def _token() -> str:
    return os.environ.get("ORVA_INTERNAL_TOKEN", "")


def _function_id() -> str:
    return os.environ.get("ORVA_FUNCTION_ID", "")


def _trace_headers() -> dict:
    """Forward trace context on every internal call so F2F / job enqueues
    stay linked into the same trace as the caller. Empty when the
    function wasn't started inside a trace (legacy or test)."""
    h: dict = {}
    if v := os.environ.get("ORVA_TRACE_ID"):
        h["X-Orva-Trace-Id"] = v
    if v := os.environ.get("ORVA_SPAN_ID"):
        h["X-Orva-Span-Id"] = v
    if v := _function_id():
        h["X-Orva-Caller-Function"] = v
    return h


def _request(method: str, path: str, *, body: bytes = b"", headers: dict | None = None) -> tuple[int, bytes]:
    base = _api_base()
    token = _token()
    if not base or not token:
        raise OrvaUnavailableError("Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)")

    h = {"X-Orva-Internal-Token": token, "Content-Type": "application/json"}
    h.update(_trace_headers())
    if headers:
        h.update(headers)

    req = urllib.request.Request(base + path, data=body or None, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.getcode(), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


# ── KV ──────────────────────────────────────────────────────────────


class _KV:
    """Per-function key/value store backed by SQLite on the host. Values
    are JSON-encoded transparently. TTL is in seconds; 0 (default) means
    the key persists until deleted."""

    @staticmethod
    def get(key: str, default=None):
        fn = _function_id()
        status, body = _request("GET", f"/api/v1/_kv/{fn}/{key}")
        if status == 404:
            return default
        if status >= 400:
            raise OrvaError(f"kv.get({key!r}) failed: {body!r}", status=status)
        data = json.loads(body)
        return json.loads(data["value"]) if data.get("value") is not None else default

    @staticmethod
    def put(key: str, value, ttl_seconds: int = 0) -> None:
        fn = _function_id()
        payload = json.dumps({
            "value": json.dumps(value),
            "ttl_seconds": int(ttl_seconds),
        }).encode("utf-8")
        status, body = _request("PUT", f"/api/v1/_kv/{fn}/{key}", body=payload)
        if status >= 400:
            raise OrvaError(f"kv.put({key!r}) failed: {body!r}", status=status)

    @staticmethod
    def delete(key: str) -> None:
        fn = _function_id()
        status, body = _request("DELETE", f"/api/v1/_kv/{fn}/{key}")
        if status >= 400 and status != 404:
            raise OrvaError(f"kv.delete({key!r}) failed: {body!r}", status=status)

    @staticmethod
    def list(prefix: str = "", limit: int = 100):
        fn = _function_id()
        path = f"/api/v1/_kv/{fn}?limit={int(limit)}"
        if prefix:
            path += f"&prefix={prefix}"
        status, body = _request("GET", path)
        if status >= 400:
            raise OrvaError(f"kv.list failed: {body!r}", status=status)
        data = json.loads(body)
        # The wire format already returns each entry's value as the
        # original JSON object — no second decode needed.
        return list(data.get("keys") or [])


kv = _KV()


# ── Function-to-function invoke ─────────────────────────────────────


def invoke(function_name: str, payload=None, *, timeout: int = 30):
    """Invoke another Orva function by friendly name. Returns the parsed
    {statusCode, headers, body} envelope so callers can branch on
    statusCode if they care. The body is JSON-decoded when possible."""

    body = json.dumps(payload if payload is not None else {}).encode("utf-8")
    headers = {}
    # Forward call depth so nested invokes cap out before deadlocking the pool.
    incoming = os.environ.get("ORVA_CALL_DEPTH", "")
    if incoming:
        headers["X-Orva-Call-Depth"] = incoming
    status, raw = _request("POST", f"/api/v1/_internal/invoke/{function_name}", body=body, headers=headers)
    if status == 404:
        raise OrvaError(f"function not found: {function_name}", status=404)
    if status == 507:
        raise OrvaError("call depth exceeded", status=507)
    if status >= 400:
        raise OrvaError(f"invoke({function_name!r}) failed: {raw!r}", status=status)

    envelope = json.loads(raw)
    body_str = envelope.get("body", "")
    if isinstance(body_str, str):
        try:
            envelope["body"] = json.loads(body_str)
        except (ValueError, TypeError):
            pass  # leave as string
    return envelope


# ── Background jobs ─────────────────────────────────────────────────


class _Jobs:
    """Fire-and-forget queue. enqueue() returns the job id."""

    @staticmethod
    def enqueue(function_name: str, payload=None, *, max_attempts: int = 3, scheduled_at=None) -> str:
        body_obj = {
            "function_name": function_name,
            "payload": payload if payload is not None else {},
            "max_attempts": int(max_attempts),
        }
        if scheduled_at is not None:
            body_obj["scheduled_at"] = scheduled_at
        status, raw = _request("POST", "/api/v1/jobs", body=json.dumps(body_obj).encode("utf-8"))
        if status >= 400:
            raise OrvaError(f"jobs.enqueue failed: {raw!r}", status=status)
        data = json.loads(raw)
        return data["id"]


jobs = _Jobs()


__all__ = ["kv", "invoke", "jobs", "OrvaError", "OrvaUnavailableError"]
