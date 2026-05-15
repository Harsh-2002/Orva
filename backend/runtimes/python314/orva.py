"""Orva Python SDK — kv, invoke, jobs, crons, trace, log, context.

Available inside any function running on Orva. Routes through the
ORVA_API_BASE loopback URL using the per-process ORVA_INTERNAL_TOKEN
that the worker received at spawn time. Both env vars are present in
production and absent in tests; helpers raise OrvaUnavailableError when
the SDK can't reach the host unless __test_mode__ has installed an
override.

Stdlib-only (urllib, json, time, os, sys) — no third-party deps and no
build step.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable, Dict, Iterator, List, Optional, TypeVar
from urllib.parse import quote


# SDK version baked at adapter-embed time. Sent on every internal call.
SDK_VERSION = "0.6.0"

T = TypeVar("T")

# ── Errors ──────────────────────────────────────────────────────────


class OrvaError(RuntimeError):
    """Base class for SDK errors. `status` carries the upstream HTTP code
    so callers can branch."""

    def __init__(self, message: str, status: int = 0):
        super().__init__(message)
        self.status = status


class OrvaUnavailableError(OrvaError):
    """ORVA_API_BASE / ORVA_INTERNAL_TOKEN are missing — typically only
    happens in tests or when running the handler outside of Orva's
    sandbox."""


class OrvaCASMismatch(OrvaError):
    """kv.cas() precondition failed. `current_value` carries the value
    that was actually present so callers can retry with a fresh
    expectation."""

    def __init__(self, current_value: Any):
        super().__init__("kv.cas: precondition failed", status=409)
        self.current_value = current_value


# ── Test-mode hook ──────────────────────────────────────────────────


_test_impl: Optional[Dict[str, Callable]] = None


def __test_mode__(impl: Optional[Dict[str, Callable]]) -> None:
    """Swap the SDK transport for unit tests. `impl` is a dict with an
    optional `request` callable matching the (method, path, opts) →
    (status, body) shape of the internal `_request` function."""
    global _test_impl
    _test_impl = impl


# ── Environment accessors ───────────────────────────────────────────


def _api_base() -> str:
    return os.environ.get("ORVA_API_BASE", "")


def _token() -> str:
    return os.environ.get("ORVA_INTERNAL_TOKEN", "")


def _function_id() -> str:
    return os.environ.get("ORVA_FUNCTION_ID", "")


def _execution_id() -> str:
    return os.environ.get("ORVA_EXECUTION_ID", "")


def _trace_id() -> str:
    return os.environ.get("ORVA_TRACE_ID", "")


def _span_id() -> str:
    return os.environ.get("ORVA_SPAN_ID", "")


def _call_depth() -> int:
    try:
        return int(os.environ.get("ORVA_CALL_DEPTH", "0"))
    except ValueError:
        return 0


def _timeout_ms() -> int:
    try:
        return int(os.environ.get("ORVA_TIMEOUT_MS", "30000"))
    except ValueError:
        return 30000


def _memory_mb() -> int:
    try:
        return int(os.environ.get("ORVA_MEMORY_MB", "64"))
    except ValueError:
        return 64


def _trace_headers() -> Dict[str, str]:
    """Forward trace context on every internal call so F2F / job enqueues
    stay linked into the same trace as the caller."""
    h: Dict[str, str] = {"X-Orva-SDK-Version": SDK_VERSION}
    if v := _trace_id():
        h["X-Orva-Trace-Id"] = v
    if v := _span_id():
        h["X-Orva-Span-Id"] = v
    if v := _function_id():
        h["X-Orva-Caller-Function"] = v
        h["X-Orva-Function-Id"] = v
    if v := _execution_id():
        h["X-Orva-Execution-Id"] = v
    return h


# ── HTTP transport ──────────────────────────────────────────────────


DEFAULT_TIMEOUT_S = 30.0

# A module-level opener with HTTP keep-alive enabled. urllib's default
# `urlopen` opens a fresh TCP connection every call; the opener pools
# connections so the loopback handshake amortises across the function's
# SDK calls. The HTTPHandler is the stock one — keep-alive support comes
# from Python 3.12+'s default `Connection: keep-alive` behaviour on
# urllib's HTTPConnection underneath.
_opener = urllib.request.build_opener()


def _request(
    method: str,
    path: str,
    *,
    body: bytes = b"",
    headers: Optional[Dict[str, str]] = None,
    timeout_s: float = DEFAULT_TIMEOUT_S,
) -> tuple[int, bytes, Dict[str, str]]:
    if _test_impl and "request" in _test_impl:
        out = _test_impl["request"](
            method, path, {"body": body, "headers": headers or {}}
        )
        if isinstance(out, tuple) and len(out) == 2:
            return out[0], out[1], {}
        return out
    base = _api_base()
    token = _token()
    if not base or not token:
        raise OrvaUnavailableError(
            "Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)"
        )
    h: Dict[str, str] = {
        "X-Orva-Internal-Token": token,
        "Content-Type": "application/json",
    }
    h.update(_trace_headers())
    if headers:
        h.update(headers)
    req = urllib.request.Request(base + path, data=body or None, headers=h, method=method)
    try:
        with _opener.open(req, timeout=timeout_s) as resp:
            return (
                resp.getcode(),
                resp.read(),
                {k.lower(): v for k, v in resp.headers.items()},
            )
    except urllib.error.HTTPError as e:
        return e.code, e.read(), {k.lower(): v for k, v in (e.headers or {}).items()}
    except (urllib.error.URLError, TimeoutError) as e:
        raise OrvaError(f"request failed: {e}", status=0)


# ── KV ──────────────────────────────────────────────────────────────


class _KV:
    """Per-function key/value store backed by SQLite on the host."""

    @staticmethod
    def get(key: str, default: Any = None) -> Any:
        fn = _function_id()
        status, body, _ = _request("GET", f"/api/v1/_kv/{fn}/{quote(key, safe='')}")
        if status == 404:
            return default
        if status >= 400:
            raise OrvaError(f"kv.get({key!r}) failed: {body!r}", status=status)
        data = json.loads(body)
        return data["value"] if data.get("value") is not None else default

    @staticmethod
    def put(key: str, value: Any, *, ttl_seconds: int = 0) -> None:
        fn = _function_id()
        payload = json.dumps({"value": value, "ttl_seconds": int(ttl_seconds)}).encode("utf-8")
        status, body, _ = _request(
            "PUT", f"/api/v1/_kv/{fn}/{quote(key, safe='')}", body=payload
        )
        if status >= 400:
            raise OrvaError(f"kv.put({key!r}) failed: {body!r}", status=status)

    @staticmethod
    def delete(key: str) -> None:
        fn = _function_id()
        status, body, _ = _request("DELETE", f"/api/v1/_kv/{fn}/{quote(key, safe='')}")
        if status >= 400 and status != 404:
            raise OrvaError(f"kv.delete({key!r}) failed: {body!r}", status=status)

    @staticmethod
    def list(
        prefix: str = "", limit: int = 100, cursor: str = ""
    ) -> Dict[str, Any]:
        fn = _function_id()
        qs = [f"limit={int(limit)}"]
        if prefix:
            qs.append("prefix=" + urllib.parse.quote(prefix, safe=""))
        if cursor:
            qs.append("cursor=" + urllib.parse.quote(cursor, safe=""))
        status, body, _ = _request("GET", f"/api/v1/_kv/{fn}?" + "&".join(qs))
        if status >= 400:
            raise OrvaError(f"kv.list failed: {body!r}", status=status)
        data = json.loads(body)
        return {"keys": list(data.get("keys") or []), "next_cursor": data.get("next_cursor", "")}

    @staticmethod
    def get_many(keys: List[str]) -> Dict[str, Any]:
        if not keys:
            return {}
        fn = _function_id()
        payload = json.dumps({"ops": [{"op": "get", "key": k} for k in keys]}).encode("utf-8")
        status, body, _ = _request("POST", f"/api/v1/_kv/{fn}/batch", body=payload)
        if status >= 400:
            raise OrvaError(f"kv.get_many failed: {body!r}", status=status)
        data = json.loads(body)
        out: Dict[str, Any] = {}
        for r in data.get("results") or []:
            out[r["key"]] = r.get("value") if r.get("found") else None
        return out

    @staticmethod
    def put_many(entries: List[Dict[str, Any]]) -> None:
        if not entries:
            return
        fn = _function_id()
        ops = [
            {
                "op": "put",
                "key": e["key"],
                "value": e["value"],
                "ttl_seconds": int(e.get("ttl_seconds", 0)),
            }
            for e in entries
        ]
        payload = json.dumps({"ops": ops}).encode("utf-8")
        status, body, _ = _request("POST", f"/api/v1/_kv/{fn}/batch", body=payload)
        if status >= 400:
            raise OrvaError(f"kv.put_many failed: {body!r}", status=status)

    @staticmethod
    def delete_many(keys: List[str]) -> int:
        if not keys:
            return 0
        fn = _function_id()
        payload = json.dumps({"ops": [{"op": "delete", "key": k} for k in keys]}).encode("utf-8")
        status, body, _ = _request("POST", f"/api/v1/_kv/{fn}/batch", body=payload)
        if status >= 400:
            raise OrvaError(f"kv.delete_many failed: {body!r}", status=status)
        data = json.loads(body)
        return sum(1 for r in data.get("results") or [] if r.get("found"))

    @staticmethod
    def incr(key: str, delta: int = 1, *, ttl_seconds: int = 0) -> int:
        fn = _function_id()
        payload = json.dumps({"delta": int(delta), "ttl_seconds": int(ttl_seconds)}).encode("utf-8")
        status, body, _ = _request(
            "POST", f"/api/v1/_kv/{fn}/{quote(key, safe='')}/incr", body=payload
        )
        if status >= 400:
            raise OrvaError(f"kv.incr({key!r}) failed: {body!r}", status=status)
        return int(json.loads(body)["value"])

    @staticmethod
    def cas(key: str, expected: Any, new: Any, *, ttl_seconds: int = 0) -> bool:
        fn = _function_id()
        payload = json.dumps(
            {"expected": expected, "new": new, "ttl_seconds": int(ttl_seconds)}
        ).encode("utf-8")
        status, body, _ = _request(
            "POST", f"/api/v1/_kv/{fn}/{quote(key, safe='')}/cas", body=payload
        )
        if status >= 400:
            raise OrvaError(f"kv.cas({key!r}) failed: {body!r}", status=status)
        data = json.loads(body)
        if not data.get("ok"):
            raise OrvaCASMismatch(data.get("current"))
        return True


kv = _KV()


# ── Function-to-function invoke ─────────────────────────────────────


def invoke(
    function_name: str,
    payload: Any = None,
    *,
    timeout_ms: int = 30000,
) -> Dict[str, Any]:
    """Invoke another Orva function by friendly name. Returns the parsed
    {statusCode, headers, body} envelope; body is JSON-decoded when
    possible."""
    body = json.dumps(payload if payload is not None else {}).encode("utf-8")
    headers: Dict[str, str] = {}
    incoming = os.environ.get("ORVA_CALL_DEPTH", "")
    if incoming:
        headers["X-Orva-Call-Depth"] = incoming
    status, raw, _ = _request(
        "POST",
        f"/api/v1/_internal/invoke/{function_name}",
        body=body,
        headers=headers,
        timeout_s=max(timeout_ms / 1000.0, 1.0),
    )
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
            pass
    return envelope


def invoke_stream(
    function_name: str,
    payload: Any = None,
    *,
    timeout_ms: int = 30000,
) -> Iterator[bytes]:
    """Stream-invoke variant. Yields response body chunks as bytes. Use
    when the callee returns SSE / async-iterator / generator output and
    you want to consume frames as they arrive instead of buffering."""
    base = _api_base()
    token = _token()
    if not base or not token:
        raise OrvaUnavailableError(
            "Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)"
        )
    body = json.dumps(payload if payload is not None else {}).encode("utf-8")
    h: Dict[str, str] = {
        "X-Orva-Internal-Token": token,
        "Content-Type": "application/json",
        "X-Orva-Call-Depth": str(_call_depth()),
    }
    h.update(_trace_headers())
    req = urllib.request.Request(
        base + f"/api/v1/_internal/invoke/{function_name}/stream",
        data=body,
        headers=h,
        method="POST",
    )
    try:
        resp = _opener.open(req, timeout=max(timeout_ms / 1000.0, 1.0))
    except urllib.error.HTTPError as e:
        if e.code == 404:
            raise OrvaError(f"function not found: {function_name}", status=404)
        if e.code == 507:
            raise OrvaError("call depth exceeded", status=507)
        raise OrvaError(f"invoke_stream({function_name!r}) failed: {e.read()!r}", status=e.code)
    except (urllib.error.URLError, TimeoutError) as e:
        raise OrvaError(f"invoke_stream request failed: {e}", status=0)
    try:
        while True:
            chunk = resp.read(64 * 1024)
            if not chunk:
                return
            yield chunk
    finally:
        resp.close()


# ── Background jobs ─────────────────────────────────────────────────


class _Jobs:
    @staticmethod
    def enqueue(
        function_name: str,
        payload: Any = None,
        *,
        max_attempts: int = 3,
        scheduled_at: Optional[str] = None,
        idempotency_key: Optional[str] = None,
        idempotency_window_seconds: int = 0,
    ) -> Dict[str, Any]:
        body_obj: Dict[str, Any] = {
            "function_name": function_name,
            "payload": payload if payload is not None else {},
            "max_attempts": int(max_attempts),
        }
        if scheduled_at is not None:
            body_obj["scheduled_at"] = scheduled_at
        if idempotency_key:
            body_obj["idempotency_key"] = idempotency_key
        if idempotency_window_seconds:
            body_obj["idempotency_window_seconds"] = int(idempotency_window_seconds)
        status, raw, headers = _request(
            "POST", "/api/v1/jobs", body=json.dumps(body_obj).encode("utf-8")
        )
        if status >= 400:
            raise OrvaError(f"jobs.enqueue failed: {raw!r}", status=status)
        data = json.loads(raw)
        replayed = headers.get("x-idempotency-replayed") == "true" or data.get("replayed") is True
        return {"id": data["id"], "replayed": bool(replayed)}


jobs = _Jobs()


# ── Cron-from-code ──────────────────────────────────────────────────


class _Crons:
    @staticmethod
    def upsert(
        name: str,
        schedule: str,
        *,
        payload: Any = None,
        timezone: Optional[str] = None,
        enabled: Optional[bool] = None,
    ) -> Dict[str, Any]:
        body_obj: Dict[str, Any] = {"name": name, "schedule": schedule}
        if payload is not None:
            body_obj["payload"] = payload
        if timezone:
            body_obj["timezone"] = timezone
        if enabled is not None:
            body_obj["enabled"] = bool(enabled)
        status, raw, _ = _request(
            "POST", "/api/v1/_internal/crons", body=json.dumps(body_obj).encode("utf-8")
        )
        if status >= 400:
            raise OrvaError(f"crons.upsert({name!r}) failed: {raw!r}", status=status)
        return json.loads(raw)


crons = _Crons()


# ── User-defined spans ──────────────────────────────────────────────


class _Trace:
    @staticmethod
    @contextmanager
    def span(name: str, attributes: Optional[Dict[str, Any]] = None) -> Iterator[None]:
        """Context manager wrapping a code block in a child span. Errors
        inside the block are recorded as status="error" and re-raised."""
        started_at = datetime.now(timezone.utc)
        t0 = time.monotonic()
        ok = True
        err_msg = ""
        try:
            yield
        except BaseException as e:  # capture every kind of exit
            ok = False
            err_msg = str(e)
            raise
        finally:
            duration_ms = int((time.monotonic() - t0) * 1000)
            body_obj: Dict[str, Any] = {
                "name": name,
                "started_at": started_at.isoformat(),
                "duration_ms": duration_ms,
                "status": "ok" if ok else "error",
            }
            if err_msg:
                body_obj["error_message"] = err_msg
            if attributes is not None:
                body_obj["attributes"] = attributes
            try:
                _request(
                    "POST",
                    "/api/v1/_internal/spans",
                    body=json.dumps(body_obj).encode("utf-8"),
                )
            except Exception:
                # Fire-and-forget — never let span ingestion break user code.
                pass


trace = _Trace()


# ── Structured logging ──────────────────────────────────────────────


def _emit_log(level: str, msg: str, fields: Optional[Dict[str, Any]]) -> None:
    rec: Dict[str, Any] = {
        "ts": datetime.now(timezone.utc).isoformat(),
        "level": level,
        "message": msg if isinstance(msg, str) else json.dumps(msg),
    }
    if fields:
        rec["fields"] = fields
    if span := _span_id():
        rec["span_id"] = span
    try:
        sys.stderr.write("__ORVA_LOG_JSON__" + json.dumps(rec) + "\n")
        sys.stderr.flush()
    except Exception:
        pass


class _Log:
    @staticmethod
    def debug(msg: str, fields: Optional[Dict[str, Any]] = None) -> None:
        _emit_log("debug", msg, fields)

    @staticmethod
    def info(msg: str, fields: Optional[Dict[str, Any]] = None) -> None:
        _emit_log("info", msg, fields)

    @staticmethod
    def warn(msg: str, fields: Optional[Dict[str, Any]] = None) -> None:
        _emit_log("warn", msg, fields)

    @staticmethod
    def error(msg: str, fields: Optional[Dict[str, Any]] = None) -> None:
        _emit_log("error", msg, fields)


log = _Log()


# ── Secrets ─────────────────────────────────────────────────────────


class _Secrets:
    @staticmethod
    def get(name: str) -> Optional[str]:
        return os.environ.get(name)


secrets = _Secrets()


# ── Webhook helper ──────────────────────────────────────────────────


def _first_header(event: Dict[str, Any], *names: str) -> str:
    h = event.get("headers") or {} if event else {}
    for n in names:
        if n in h:
            return str(h[n])
        if n.lower() in h:
            return str(h[n.lower()])
        if n.upper() in h:
            return str(h[n.upper()])
    return ""


class _Webhook:
    @staticmethod
    def parse(event: Dict[str, Any]) -> Dict[str, Any]:
        headers = (event.get("headers") or {}) if event else {}
        trigger = _first_header(event, "x-orva-trigger")
        webhook_id = _first_header(event, "x-orva-inbound-webhook-id")
        source = "unknown"
        event_type = ""
        if _first_header(event, "X-GitHub-Event"):
            source = "github"
            event_type = _first_header(event, "X-GitHub-Event")
        elif _first_header(event, "Stripe-Signature"):
            source = "stripe"
            event_type = _first_header(event, "Stripe-Event-Type")
        elif _first_header(event, "X-Slack-Signature"):
            source = "slack"
        elif _first_header(event, "X-Hub-Signature-256") or _first_header(event, "X-Signature"):
            source = "hmac"
        payload: Any = event.get("body") if event else None
        if isinstance(payload, str) and payload:
            try:
                payload = json.loads(payload)
            except (ValueError, TypeError):
                pass
        return {
            "verified": trigger == "inbound_webhook",
            "source": source,
            "event_type": event_type,
            "webhook_id": webhook_id,
            "payload": payload,
            "headers": headers,
        }


webhook = _Webhook()


# ── Context (env-var snapshot, lazy) ────────────────────────────────


class _Context:
    @property
    def function_id(self) -> str: return _function_id()

    @property
    def execution_id(self) -> str: return _execution_id()

    @property
    def trace_id(self) -> str: return _trace_id()

    @property
    def span_id(self) -> str: return _span_id()

    @property
    def call_depth(self) -> int: return _call_depth()

    @property
    def timeout_ms(self) -> int: return _timeout_ms()

    @property
    def memory_mb(self) -> int: return _memory_mb()

    @property
    def sdk_version(self) -> str: return SDK_VERSION


context = _Context()


__all__ = [
    "kv",
    "invoke",
    "invoke_stream",
    "jobs",
    "crons",
    "trace",
    "log",
    "secrets",
    "webhook",
    "context",
    "OrvaError",
    "OrvaUnavailableError",
    "OrvaCASMismatch",
    "__test_mode__",
    "SDK_VERSION",
]
