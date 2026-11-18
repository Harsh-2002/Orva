"""Orva Python SDK — kv, invoke, jobs, crons, trace, log, context.

Uses only the inherited worker SDK channel. Without it, operations raise
OrvaUnavailableError unless __test_mode__ has installed an override.
Stdlib-only, with no third-party deps or build step.
"""

from __future__ import annotations

import base64
import json
import os
import stat
import struct
import sys
import threading
import time
import urllib.parse
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Awaitable, Callable, Dict, Iterator, List, Optional, TypeVar
from urllib.parse import quote


# SDK version baked at adapter-embed time.
SDK_VERSION = "0.7.0"

T = TypeVar("T")

# ── Errors ──────────────────────────────────────────────────────────


class OrvaError(RuntimeError):
    """Base class for SDK errors. `status` carries the upstream HTTP code
    so callers can branch."""

    def __init__(self, message: str, status: int = 0):
        super().__init__(message)
        self.status = status


class OrvaUnavailableError(OrvaError):
    """The inherited worker SDK channel is unavailable."""

    def __init__(self, message: str, status: int = 0):
        super().__init__(
            f"{message}. Check the function's sdk permission and refresh the runtime adapter.",
            status=status,
        )


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
    (status, body[, headers]) shape. This hook never opens a connection."""
    global _test_impl
    _test_impl = impl


# ── Environment accessors ───────────────────────────────────────────


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


DEFAULT_TIMEOUT_S = 30.0


# ── SDK channel ─────────────────────────────────────────────────────
#
# A private AF_UNIX socketpair the host passes to the worker as fd
# ORVA_SDK_FD (with ORVA_SDK_PROTO=1). Frames are [u32 BE length][JSON];
# requests {id, op, lease?, a}, replies {id, ok, r | e:{status,code,message}}.
# The client never sends identity: the host derives it from the connection
# and the lease. Missing or invalid channel configuration fails closed.
#
# os.read/os.write ONLY: socket.recv/send compile to recvfrom/sendto, which
# the no-network seccomp profile blocks. One lazily started daemon reader
# thread demultiplexes replies by id, so threads (and asyncio handlers
# calling the synchronous SDK) can call concurrently. It touches nothing but
# this fd, so it cannot interfere with the adapter's stdout lock/heartbeat.

MAX_FRAME = 4 << 20  # 4 MiB, both directions
MAX_INFLIGHT = 32  # the host refuses the 33rd with 429, so wait client-side

# Per-dispatch lease from the adapter's `lease` stdin frame. Module-scope code
# runs before any lease, so it is empty then. The host revokes it; we never
# clear it ourselves.
_lease = ""


def _set_lease(lease: Any) -> None:
    global _lease
    _lease = lease if isinstance(lease, str) else ""


# Per-call deadline includes slot wait.
_channel_timeout_s = DEFAULT_TIMEOUT_S


def _set_channel_timeout(seconds: float) -> None:
    global _channel_timeout_s
    _channel_timeout_s = seconds


class _Chan:
    def __init__(self, fd: int):
        os.set_blocking(fd, True)
        os.set_inheritable(fd, False)
        self.fd = fd
        self.cond = threading.Condition()  # guards inflight, next_id, dead, reader
        self.wlock = threading.Lock()  # serialises frame writes
        # id -> [Event, result list, abandoned]. A timed-out entry keeps its
        # slot until the host answers it, so we never exceed MAX_INFLIGHT.
        self.inflight: Dict[int, list] = {}
        self.next_id = 1
        self.dead: Optional[OrvaError] = None
        self.reader: Optional[threading.Thread] = None

    # -- lifecycle -------------------------------------------------------

    def mark_dead(self, err: OrvaError) -> None:
        with self.cond:
            if self.dead is None:
                self.dead = err
            waiters = list(self.inflight.values())
            self.inflight.clear()
            self.cond.notify_all()
        for slot in waiters:
            slot[0].set()

    def _read_exact(self, n: int) -> bytes:
        parts = []
        have = 0
        while have < n:
            chunk = os.read(self.fd, n - have)
            if not chunk:
                raise OrvaUnavailableError("SDK channel closed")
            parts.append(chunk)
            have += len(chunk)
        return b"".join(parts)

    def _reader(self) -> None:
        try:
            while True:
                (n,) = struct.unpack(">I", self._read_exact(4))
                if n < 1 or n > MAX_FRAME:
                    raise OrvaUnavailableError(f"SDK channel protocol error: bad frame length {n}")
                try:
                    msg = json.loads(self._read_exact(n).decode("utf-8"))
                except ValueError:
                    raise OrvaUnavailableError("SDK channel protocol error: invalid JSON")
                if not isinstance(msg, dict):
                    raise OrvaUnavailableError("SDK channel protocol error: reply is not an object")
                with self.cond:
                    slot = self.inflight.pop(msg.get("id"), None)
                    self.cond.notify_all()
                if slot is not None and not slot[2]:
                    slot[1].append(msg)
                    slot[0].set()
        except BaseException as e:  # noqa: BLE001 - wake every waiter on any failure
            self.mark_dead(e if isinstance(e, OrvaError) else OrvaUnavailableError(f"SDK channel error: {e}"))
            # The fd stays open: closing it could let the number be reused
            # while a racing writer still holds it.

    # -- calls -----------------------------------------------------------

    def call(
        self, op: str, args: Dict[str, Any], timeout_s: Optional[float] = None
    ) -> tuple[int, bytes, Dict[str, str]]:
        if timeout_s is None:
            timeout_s = _channel_timeout_s
        deadline = time.monotonic() + timeout_s
        timeout_err = OrvaError(f"request failed: timed out after {timeout_s:g}s", status=0)
        with self.cond:
            if self.dead:
                raise self.dead
            cid = self.next_id
            self.next_id += 1
            if self.reader is None:
                self.reader = threading.Thread(target=self._reader, name="orva-sdk-channel", daemon=True)
                self.reader.start()
        msg: Dict[str, Any] = {"id": cid, "op": op, "a": args}
        if _lease:
            msg["lease"] = _lease
        try:
            # NaN/Infinity are not JSON: the host would treat the frame as a protocol
            # violation and drop the worker, so refuse them here.
            body = json.dumps(msg, allow_nan=False).encode("utf-8")
        except ValueError as e:
            raise OrvaError(f"{op}: value is not JSON-serialisable ({e})", status=400)
        if len(body) > MAX_FRAME:
            raise OrvaError(f"{op}: request too large ({len(body)} bytes, max {MAX_FRAME})", status=413)
        frame = struct.pack(">I", len(body)) + body

        slot = [threading.Event(), [], False]
        with self.cond:
            while len(self.inflight) >= MAX_INFLIGHT and not self.dead:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise timeout_err
                self.cond.wait(remaining)
            if self.dead:
                raise self.dead
            self.inflight[cid] = slot
        try:
            with self.wlock:
                view = memoryview(frame)
                while view:
                    view = view[os.write(self.fd, view):]
        except OSError as e:
            err = OrvaUnavailableError(f"SDK channel write failed: {e}")
            self.mark_dead(err)
            raise err

        if not slot[0].wait(max(0.0, deadline - time.monotonic())):
            slot[2] = True  # abandoned: the slot is freed when the host replies
            raise timeout_err
        if not slot[1]:
            raise self.dead or OrvaUnavailableError("SDK channel closed")
        reply = slot[1][0]
        if reply.get("ok"):
            r = reply.get("r")
            return 200, json.dumps({} if r is None else r).encode("utf-8"), {}
        e = reply.get("e") or {}
        try:
            status = int(e.get("status") or 500)
        except (TypeError, ValueError):
            status = 500
        return status, f"{e.get('code') or 'ERROR'}: {e.get('message') or ''}".encode("utf-8"), {}


_chan: Optional[_Chan] = None
_chan_probed = False
_chan_lock = threading.Lock()
_channel_forked = False


def _channel() -> Optional[_Chan]:
    """The open channel, or None when this worker has none."""
    global _chan, _chan_probed
    if _channel_forked and os.environ.get("ORVA_SDK_PROTO") == "1":
        raise OrvaUnavailableError("SDK channel unavailable in a forked child")
    if _chan_probed:
        return _chan
    with _chan_lock:
        if _chan_probed:
            return _chan
        try:
            if os.environ.get("ORVA_SDK_PROTO") != "1":
                return None
            raw = os.environ.get("ORVA_SDK_FD", "")
            if not raw.isascii() or not raw.isdigit():
                return None
            try:
                fd = int(raw)
                if fd < 3:
                    return None
                if not stat.S_ISSOCK(os.fstat(fd).st_mode):
                    return None
                _chan = _Chan(fd)
            except (OSError, ValueError, OverflowError):
                _chan = None
        finally:
            # Publish only after initialization; other threads must not observe
            # a completed probe while the channel is still being constructed.
            _chan_probed = True
        return _chan


def _after_fork_in_child() -> None:
    # A forked child shares the descriptor with its parent and has no reader
    # thread; two readers on one stream would steal each other's frames, so the
    # child must never use it.
    global _channel_forked
    _channel_forked = True
    ch = _chan
    if ch is not None:
        ch.dead = OrvaUnavailableError("SDK channel unavailable in a forked child")
        ch.inflight.clear()
        ch.cond = threading.Condition()
        ch.wlock = threading.Lock()


if hasattr(os, "register_at_fork"):
    os.register_at_fork(after_in_child=_after_fork_in_child)


def _require_channel() -> _Chan:
    ch = _channel()
    if ch is None:
        raise OrvaUnavailableError("Orva SDK channel unavailable (missing or invalid descriptor/protocol)")
    return ch


def _kv(
    op: str,
    args: Dict[str, Any],
    method: str,
    path: str,
    body: bytes = b"",
    headers: Optional[Dict[str, str]] = None,
    timeout_s: Optional[float] = None,
) -> tuple[int, bytes, Dict[str, str]]:
    """Method/path metadata exists only for the public unit-test hook."""
    if _test_impl and "request" in _test_impl:
        out = _test_impl["request"](method, path, {"body": body, "headers": headers or {}})
        if isinstance(out, tuple) and len(out) == 2:
            return out[0], out[1], {}
        return out
    return _require_channel().call(op, args, timeout_s)


# ── KV ──────────────────────────────────────────────────────────────


class _KV:
    """Per-function key/value store backed by SQLite on the host."""

    @staticmethod
    def get(key: str, default: Any = None) -> Any:
        fn = _function_id()
        status, body, _ = _kv(
            "kv.get", {"key": key}, "GET", f"/api/v1/_kv/{fn}/{quote(key, safe='')}"
        )
        if status == 404:
            return default
        if status >= 400:
            raise OrvaError(f"kv.get({key!r}) failed: {body!r}", status=status)
        data = json.loads(body)
        return data["value"] if data.get("value") is not None else default

    @staticmethod
    def put(key: str, value: Any, *, ttl_seconds: Optional[int] = None) -> None:
        fn = _function_id()
        request_body: Dict[str, Any] = {"value": value}
        if ttl_seconds is not None:
            request_body["ttl_seconds"] = int(ttl_seconds)
        payload = json.dumps(request_body).encode("utf-8")
        status, body, _ = _kv(
            "kv.put",
            {"key": key, **request_body},
            "PUT",
            f"/api/v1/_kv/{fn}/{quote(key, safe='')}",
            body=payload,
        )
        if status >= 400:
            raise OrvaError(f"kv.put({key!r}) failed: {body!r}", status=status)

    @staticmethod
    def delete(key: str) -> None:
        fn = _function_id()
        status, body, _ = _kv(
            "kv.delete", {"key": key}, "DELETE", f"/api/v1/_kv/{fn}/{quote(key, safe='')}"
        )
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
        args: Dict[str, Any] = {"limit": int(limit)}
        if prefix:
            args["prefix"] = prefix
        if cursor:
            args["cursor"] = cursor
        status, body, _ = _kv("kv.list", args, "GET", f"/api/v1/_kv/{fn}?" + "&".join(qs))
        if status >= 400:
            raise OrvaError(f"kv.list failed: {body!r}", status=status)
        data = json.loads(body)
        _assert_atomic_batch(data, "kv.get_many")
        return {"keys": list(data.get("keys") or []), "next_cursor": data.get("next_cursor", "")}

    @staticmethod
    def get_many(keys: List[str]) -> Dict[str, Any]:
        if not keys:
            return {}
        fn = _function_id()
        ops = [{"op": "get", "key": k} for k in keys]
        status, body, _ = _kv_batch(fn, ops)
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
        ops = []
        for entry in entries:
            op = {"op": "put", "key": entry["key"], "value": entry["value"]}
            if "ttl_seconds" in entry:
                op["ttl_seconds"] = int(entry["ttl_seconds"])
            ops.append(op)
        status, body, _ = _kv_batch(fn, ops)
        if status >= 400:
            raise OrvaError(f"kv.put_many failed: {body!r}", status=status)
        _assert_atomic_batch(json.loads(body), "kv.put_many")

    @staticmethod
    def delete_many(keys: List[str]) -> int:
        if not keys:
            return 0
        fn = _function_id()
        ops = [{"op": "delete", "key": k} for k in keys]
        status, body, _ = _kv_batch(fn, ops)
        if status >= 400:
            raise OrvaError(f"kv.delete_many failed: {body!r}", status=status)
        data = json.loads(body)
        _assert_atomic_batch(data, "kv.delete_many")
        return sum(1 for r in data.get("results") or [] if r.get("found"))

    @staticmethod
    def incr(key: str, delta: int = 1, *, ttl_seconds: Optional[int] = None) -> int:
        fn = _function_id()
        request_body: Dict[str, Any] = {"delta": int(delta)}
        if ttl_seconds is not None:
            request_body["ttl_seconds"] = int(ttl_seconds)
        payload = json.dumps(request_body).encode("utf-8")
        status, body, _ = _kv(
            "kv.incr",
            {"key": key, **request_body},
            "POST",
            f"/api/v1/_kv/{fn}/{quote(key, safe='')}/incr",
            body=payload,
        )
        if status >= 400:
            raise OrvaError(f"kv.incr({key!r}) failed: {body!r}", status=status)
        return int(json.loads(body)["value"])

    @staticmethod
    def cas(key: str, expected: Any, new: Any, *, ttl_seconds: Optional[int] = None) -> bool:
        fn = _function_id()
        request_body: Dict[str, Any] = {"expected": expected, "new": new}
        if ttl_seconds is not None:
            request_body["ttl_seconds"] = int(ttl_seconds)
        payload = json.dumps(request_body).encode("utf-8")
        status, body, _ = _kv(
            "kv.cas",
            {"key": key, **request_body},
            "POST",
            f"/api/v1/_kv/{fn}/{quote(key, safe='')}/cas",
            body=payload,
        )
        if status >= 400:
            raise OrvaError(f"kv.cas({key!r}) failed: {body!r}", status=status)
        data = json.loads(body)
        if not data.get("ok"):
            raise OrvaCASMismatch(data.get("current"))
        return True


kv = _KV()


def _kv_batch(fn: str, ops: List[Dict[str, Any]]) -> tuple[int, bytes, Dict[str, str]]:
    return _kv(
        "kv.batch",
        {"ops": ops},
        "POST",
        f"/api/v1/_kv/{fn}/batch",
        body=json.dumps({"ops": ops}).encode("utf-8"),
    )


def _assert_atomic_batch(data: Dict[str, Any], operation: str) -> None:
    failed = next((item for item in data.get("results") or [] if item.get("error")), None)
    if failed:
        raise OrvaError(f"{operation} failed: {failed['error']}", status=500)


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
    status, raw, _ = _kv(
        "invoke",
        {"name": function_name, "body": payload if payload is not None else {}},
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
    yield from _invoke_stream_channel(
        _require_channel(), function_name, payload, max(timeout_ms / 1000.0, 1.0)
    )


def _invoke_stream_channel(
    ch: _Chan, function_name: str, payload: Any, timeout_s: float
) -> Iterator[bytes]:
    """invoke_stream over the SDK channel: open, then one `next` per chunk.
    Closing the generator early sends invoke.stream.close."""
    status, raw, _ = ch.call(
        "invoke.stream.open",
        {"name": function_name, "body": payload if payload is not None else {}},
        timeout_s,
    )
    if status == 404:
        raise OrvaError(f"function not found: {function_name}", status=404)
    if status == 507:
        raise OrvaError("call depth exceeded", status=507)
    if status >= 400:
        raise OrvaError(f"invoke_stream({function_name!r}) failed: {raw!r}", status=status)
    handle: Optional[str] = json.loads(raw)["handle"]
    try:
        while True:
            status, raw, _ = ch.call("invoke.stream.next", {"handle": handle}, timeout_s)
            if status >= 400:
                handle = None  # the host already closed it after a mid-stream failure
                raise OrvaError(f"invoke_stream({function_name!r}) failed: {raw!r}", status=status)
            data = json.loads(raw)
            chunk = base64.b64decode(data.get("chunk_b64") or "")
            done = bool(data.get("done"))
            if done:
                handle = None
            if chunk:
                yield chunk
            if done:
                return
    finally:
        if handle is not None:
            try:
                ch.call("invoke.stream.close", {"handle": handle}, timeout_s)
            except Exception:  # noqa: BLE001 - best effort, the host also closes on lease end
                pass


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
        status, raw, headers = _kv(
            "jobs.enqueue", body_obj, "POST", "/api/v1/jobs", body=json.dumps(body_obj).encode("utf-8")
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
        status, raw, _ = _kv(
            "crons.upsert", body_obj, "POST", "/api/v1/_internal/crons",
            body=json.dumps(body_obj).encode("utf-8"),
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
                _kv(
                    "spans.emit",
                    body_obj,
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
