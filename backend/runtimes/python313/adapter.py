"""Orva Python adapter — universal handler loader.

Accepts a wide range of conventions so existing code from AWS Lambda,
Google Cloud Functions, Azure, FastAPI, Flask, Django, Starlette, and
generic Python deployments runs with zero changes:

    AWS Lambda      : def lambda_handler(event, context): ...
                      def handler(event, context): ...
    GCP Functions   : def main(request): ...               (Flask Request)
    Azure Functions : def main(req): ...                   (HttpRequest)
    ASGI (FastAPI,
      Starlette)    : app = FastAPI()  /  app = Starlette()
    WSGI (Flask,
      Django)       : app = Flask(__name__)  /  app = ...   (WSGI callable)
    Plain           : def handler(event): ...

Response normalisation: the adapter accepts dicts in the Orva envelope
({statusCode, headers, body}), Starlette/FastAPI Response objects, Flask
Response objects, plain strings/dicts, or any ASGI app response. Everything
ends up as a single {statusCode, headers, body} JSON payload written to
stdout for the Orva proxy.
"""

import asyncio
import base64
import importlib.util
import inspect
import json
import os
import sys
import threading
import time
import traceback

FUNCTION_DIR = "/code"
entrypoint = os.environ.get("ORVA_ENTRYPOINT", "handler.py")
handler_path = os.path.join(FUNCTION_DIR, entrypoint)

# Preserve the real stdout for the protocol response; reroute user print().
protocol_stdout = sys.stdout
sys.stdout = sys.stderr

if FUNCTION_DIR not in sys.path:
    sys.path.insert(0, FUNCTION_DIR)

# Make the bundled `orva` SDK (kv / invoke / jobs) importable from user
# code as `from orva import kv, invoke, jobs`. /opt/orva is the dir
# adapter.py itself runs from, but Python only auto-adds it to sys.path
# when invoked as `python /opt/orva/adapter.py` AND nothing has
# rewritten sys.path[0]. Insert explicitly so the import works
# regardless of how the adapter was invoked.
if "/opt/orva" not in sys.path:
    sys.path.insert(0, "/opt/orva")


class _Context:
    """Minimal AWS-Lambda-like context object."""

    def __init__(self, event):
        hdrs = event.get("headers", {}) if isinstance(event, dict) else {}
        self.function_name = os.environ.get("ORVA_FUNCTION_NAME", "")
        self.aws_request_id = hdrs.get("x-orva-execution-id", "")
        self.invoked_function_arn = ""
        self.memory_limit_in_mb = os.environ.get("ORVA_MEMORY_MB", "")
        self.log_group_name = "orva"
        self.log_stream_name = hdrs.get("x-orva-execution-id", "")

    def get_remaining_time_in_millis(self):
        return int(os.environ.get("ORVA_TIMEOUT_MS", "30000"))


def _load_module():
    if not os.path.exists(handler_path):
        print(f"Handler not found at {handler_path}", file=sys.stderr)
        sys.exit(1)
    spec = importlib.util.spec_from_file_location("user_handler", handler_path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _resolve(mod):
    """Return (callable, style) where style is one of:
    'lambda', 'asgi', 'wsgi', 'gcp_flask_request', 'plain'.
    """
    # ASGI frameworks (FastAPI, Starlette, Quart) — `app` is an ASGI callable.
    for name in ("app", "application"):
        app = getattr(mod, name, None)
        if app is None:
            continue
        # ASGI apps have a __call__(scope, receive, send) signature and are
        # usually async. Detect by presence of `__call__` accepting 3 args.
        if callable(app):
            sig = None
            try:
                sig = inspect.signature(app.__call__ if hasattr(app, "__call__") else app)
            except (TypeError, ValueError):
                pass
            if sig and len(sig.parameters) == 3:
                return (app, "asgi")
            # Fall through — treat as WSGI (Flask, Django WSGI, etc.).
            return (app, "wsgi")

    # Function-style exports, in priority order.
    for name in ("handler", "lambda_handler", "main"):
        fn = getattr(mod, name, None)
        if callable(fn):
            return (fn, "lambda")

    return (None, None)


mod = _load_module()
handler, style = _resolve(mod)

if handler is None:
    print(
        f"Module at {handler_path} does not export a usable handler. "
        f"Expected one of: handler, lambda_handler, main, or an ASGI/WSGI `app`.",
        file=sys.stderr,
    )
    sys.exit(1)


# ── Response normalisation ─────────────────────────────────────────────

def _normalise_response(ret):
    """Convert whatever the handler returned into (status, headers, body)."""
    # Already an Orva envelope.
    if isinstance(ret, dict) and "statusCode" in ret:
        status = ret.get("statusCode", 200)
        headers = ret.get("headers", {"Content-Type": "application/json"})
        body = ret.get("body", "")
        if not isinstance(body, str):
            body = json.dumps(body)
        return (status, headers, body)

    # Starlette / FastAPI Response objects.
    if hasattr(ret, "status_code") and hasattr(ret, "body"):
        body = ret.body
        if isinstance(body, (bytes, bytearray)):
            body = body.decode("utf-8", errors="replace")
        headers = {}
        if hasattr(ret, "headers"):
            try:
                for k, v in ret.headers.items():
                    headers[k] = v
            except Exception:
                pass
        return (ret.status_code, headers, body or "")

    # Flask Response objects.
    if hasattr(ret, "status_code") and hasattr(ret, "get_data"):
        body = ret.get_data(as_text=True)
        headers = dict(ret.headers) if hasattr(ret, "headers") else {}
        return (ret.status_code, headers, body)

    # Plain string, dict, or anything else.
    if isinstance(ret, str):
        return (200, {"Content-Type": "text/plain"}, ret)
    return (200, {"Content-Type": "application/json"}, json.dumps(ret))


# ── Invocation bridges ─────────────────────────────────────────────────

def _build_flask_request(event):
    """Build a werkzeug Request (for GCP Functions / flask-style `main(request)`)."""
    try:
        from werkzeug.wrappers import Request
        from werkzeug.test import EnvironBuilder
    except ImportError:
        return None
    body = event.get("body") or ""
    builder = EnvironBuilder(
        method=event.get("method", "POST"),
        path=event.get("path", "/"),
        headers=event.get("headers", {}),
        data=body.encode("utf-8") if isinstance(body, str) else body,
    )
    return Request(builder.get_environ())


async def _call_asgi(app, event):
    """Drive an ASGI app through one request/response cycle."""
    body_bytes = (event.get("body") or "").encode("utf-8")
    headers_list = [
        (k.lower().encode(), str(v).encode())
        for k, v in (event.get("headers") or {}).items()
    ]
    path = event.get("path", "/") or "/"
    query = ""
    if "?" in path:
        path, query = path.split("?", 1)

    scope = {
        "type": "http",
        "asgi": {"version": "3.0", "spec_version": "2.3"},
        "http_version": "1.1",
        "method": event.get("method", "GET"),
        "scheme": "http",
        "path": path,
        "raw_path": path.encode(),
        "query_string": query.encode(),
        "headers": headers_list,
        "server": ("orva", 8443),
        "client": ("127.0.0.1", 0),
    }

    body_sent = False

    async def receive():
        nonlocal body_sent
        if body_sent:
            return {"type": "http.disconnect"}
        body_sent = True
        return {"type": "http.request", "body": body_bytes, "more_body": False}

    response = {"status": 200, "headers": {}, "body": b""}

    async def send(message):
        if message["type"] == "http.response.start":
            response["status"] = message["status"]
            for k, v in message.get("headers", []):
                response["headers"][k.decode()] = v.decode()
        elif message["type"] == "http.response.body":
            response["body"] += message.get("body", b"")

    await app(scope, receive, send)
    return (
        response["status"],
        response["headers"],
        response["body"].decode("utf-8", errors="replace"),
    )


def _call_wsgi(app, event):
    """Drive a WSGI app (Flask, Django WSGI) through one request cycle."""
    from io import BytesIO

    body = event.get("body") or ""
    body_bytes = body.encode("utf-8") if isinstance(body, str) else body
    path = event.get("path", "/") or "/"
    query = ""
    if "?" in path:
        path, query = path.split("?", 1)

    environ = {
        "REQUEST_METHOD": event.get("method", "GET"),
        "SCRIPT_NAME": "",
        "PATH_INFO": path,
        "QUERY_STRING": query,
        "SERVER_NAME": "orva",
        "SERVER_PORT": "8443",
        "SERVER_PROTOCOL": "HTTP/1.1",
        "wsgi.version": (1, 0),
        "wsgi.url_scheme": "http",
        "wsgi.input": BytesIO(body_bytes),
        "wsgi.errors": sys.stderr,
        "wsgi.multithread": False,
        "wsgi.multiprocess": False,
        "wsgi.run_once": True,
        "CONTENT_LENGTH": str(len(body_bytes)),
    }
    for k, v in (event.get("headers") or {}).items():
        key = "HTTP_" + k.upper().replace("-", "_")
        environ[key] = str(v)
        if k.lower() == "content-type":
            environ["CONTENT_TYPE"] = str(v)

    captured = {"status": "200 OK", "headers": []}

    def start_response(status, headers, exc_info=None):
        captured["status"] = status
        captured["headers"] = headers
        return lambda x: None

    chunks = app(environ, start_response)
    body_out = b"".join(chunks if not isinstance(chunks, (bytes, str)) else [chunks])
    if isinstance(body_out, str):
        body_out = body_out.encode("utf-8")

    status_code = int(captured["status"].split(" ", 1)[0])
    headers_dict = {k: v for k, v in captured["headers"]}
    return (status_code, headers_dict, body_out.decode("utf-8", errors="replace"))


# ── Framed stdio protocol ──────────────────────────────────────────────
# Wire format: 4-byte big-endian uint32 length, then N bytes UTF-8 JSON.
# Same on stdin (proxy → adapter) and stdout (adapter → proxy).

import struct

_stdin = sys.stdin.buffer
_stdout = protocol_stdout.buffer if hasattr(protocol_stdout, "buffer") else protocol_stdout


def _read_exact(n):
    buf = bytearray()
    while len(buf) < n:
        chunk = _stdin.read(n - len(buf))
        if not chunk:
            return None
        buf.extend(chunk)
    return bytes(buf)


def _read_frame():
    header = _read_exact(4)
    if header is None:
        return None
    (length,) = struct.unpack(">I", header)
    if length == 0:
        return {}
    payload = _read_exact(length)
    if payload is None:
        return None
    try:
        return json.loads(payload.decode("utf-8"))
    except Exception:
        return {"type": "request", "event": {"method": "POST", "path": "/", "headers": {}, "body": ""}}


# Stdout writes must be serialised — the heartbeat thread (sync generators)
# and the foreground yield-loop both write frames. JSON+length-prefix means
# any interleave corrupts the wire. The lock is uncontended on the hot
# path (one writer at a time when no heartbeat is firing).
_stdout_lock = threading.Lock()


def _write_frame(obj):
    body = json.dumps(obj).encode("utf-8")
    with _stdout_lock:
        try:
            _stdout.write(struct.pack(">I", len(body)))
            _stdout.write(body)
            _stdout.flush()
        except (BrokenPipeError, OSError):
            # The proxy went away — typically because the HTTP client
            # disconnected mid-stream. Re-raise so the caller can stop
            # iterating; the worker process exit then unblocks the pool.
            raise


def _stream_chunk(data):
    """Send a single chunk frame. data may be bytes / bytearray / str."""
    if isinstance(data, (bytes, bytearray)):
        b = bytes(data)
    elif isinstance(data, str):
        b = data.encode("utf-8")
    elif data is None:
        b = b""
    else:
        # Any other type: best-effort JSON-encode then utf-8.
        b = json.dumps(data).encode("utf-8")
    encoded = base64.b64encode(b).decode("ascii") if b else ""
    _write_frame({"type": "chunk", "data": encoded})


def _looks_like_head(item):
    """First yield can carry the response head if it's an Orva-shaped dict
    with statusCode (and no body, or with body that we treat as the first
    chunk). Returns (status, headers, leftover_body_or_None) on match,
    None otherwise."""
    if not isinstance(item, dict):
        return None
    if "statusCode" not in item:
        return None
    status = item.get("statusCode", 200)
    headers = item.get("headers", {"Content-Type": "text/plain"})
    body = item.get("body", None)
    return (status, headers, body)


def _stream_iterable(iterable, streaming_enabled, keepalive_s):
    """Drive a sync iterable / generator through the streaming protocol.

    If streaming_enabled is False we buffer everything into a single
    response frame for back-compat — operators flipping the system_config
    flag get the pre-C1 single-shot behaviour without redeploying.

    A separate thread fires an empty chunk every keepalive_s seconds if
    no real chunk has flown in that window, so intermediate proxies / LBs
    don't kill the connection during slow phases (LLM token generation,
    DB cursor walks). The thread reads last_emit under no lock — the
    timestamp is a single 64-bit float so torn reads aren't a concern.
    """
    if not streaming_enabled:
        # Fallback: buffer the entire generator output into a single
        # response. Tries to honor an Orva-shaped first item as the head;
        # otherwise wraps everything into text/plain.
        head = None
        body_parts = []
        for item in iterable:
            if head is None:
                detected = _looks_like_head(item)
                if detected is not None:
                    status, headers, body = detected
                    head = (status, headers)
                    if body is not None:
                        body_parts.append(body if isinstance(body, str) else str(body))
                    continue
                head = (200, {"Content-Type": "text/plain"})
            if isinstance(item, (bytes, bytearray)):
                body_parts.append(item.decode("utf-8", errors="replace"))
            else:
                body_parts.append(item if isinstance(item, str) else str(item))
        if head is None:
            head = (200, {"Content-Type": "text/plain"})
        status, headers = head
        _write_frame({
            "type": "response", "statusCode": status,
            "headers": headers, "body": "".join(body_parts),
        })
        return

    # Streaming path.
    head_sent = False
    last_emit = [time.monotonic()]
    stop_evt = threading.Event()

    def _heartbeat():
        while not stop_evt.wait(keepalive_s):
            if time.monotonic() - last_emit[0] >= keepalive_s:
                try:
                    _write_frame({"type": "chunk", "data": ""})
                    last_emit[0] = time.monotonic()
                except Exception:
                    return

    hb = None

    def _send_head(status, headers):
        nonlocal head_sent
        if head_sent:
            return
        _write_frame({
            "type": "response_start",
            "statusCode": status,
            "headers": headers,
        })
        head_sent = True

    try:
        for item in iterable:
            if not head_sent:
                detected = _looks_like_head(item)
                if detected is not None:
                    status, headers, body = detected
                    _send_head(status, headers)
                    # Start the heartbeat AFTER the head so the empty
                    # chunk frames never precede the response_start.
                    hb = threading.Thread(target=_heartbeat, daemon=True)
                    hb.start()
                    if body is not None and body != "":
                        _stream_chunk(body)
                        last_emit[0] = time.monotonic()
                    continue
                _send_head(200, {"Content-Type": "text/plain; charset=utf-8"})
                hb = threading.Thread(target=_heartbeat, daemon=True)
                hb.start()
            _stream_chunk(item)
            last_emit[0] = time.monotonic()
        if not head_sent:
            _send_head(200, {"Content-Type": "text/plain; charset=utf-8"})
        _write_frame({"type": "response_end"})
    except (BrokenPipeError, OSError):
        # Client disconnected. Stop iterating; the worker continues
        # serving subsequent requests if its stdin is still open.
        pass
    finally:
        stop_evt.set()


async def _stream_async_iterable(aiterable, streaming_enabled, keepalive_s):
    """Async-gen variant. Uses an asyncio Task as the heartbeat instead
    of a thread so it cooperates with the same event loop as the user
    code (no GIL contention, no thread-safe-stdout double-locking).
    """
    if not streaming_enabled:
        head = None
        body_parts = []
        async for item in aiterable:
            if head is None:
                detected = _looks_like_head(item)
                if detected is not None:
                    status, headers, body = detected
                    head = (status, headers)
                    if body is not None:
                        body_parts.append(body if isinstance(body, str) else str(body))
                    continue
                head = (200, {"Content-Type": "text/plain"})
            if isinstance(item, (bytes, bytearray)):
                body_parts.append(item.decode("utf-8", errors="replace"))
            else:
                body_parts.append(item if isinstance(item, str) else str(item))
        if head is None:
            head = (200, {"Content-Type": "text/plain"})
        status, headers = head
        _write_frame({
            "type": "response", "statusCode": status,
            "headers": headers, "body": "".join(body_parts),
        })
        return

    head_sent = [False]
    last_emit = [time.monotonic()]

    def _send_head(status, headers):
        if head_sent[0]:
            return
        _write_frame({
            "type": "response_start",
            "statusCode": status,
            "headers": headers,
        })
        head_sent[0] = True

    async def _heartbeat():
        try:
            while True:
                await asyncio.sleep(keepalive_s)
                if time.monotonic() - last_emit[0] >= keepalive_s:
                    _write_frame({"type": "chunk", "data": ""})
                    last_emit[0] = time.monotonic()
        except asyncio.CancelledError:
            return

    hb_task = None
    try:
        async for item in aiterable:
            if not head_sent[0]:
                detected = _looks_like_head(item)
                if detected is not None:
                    status, headers, body = detected
                    _send_head(status, headers)
                    hb_task = asyncio.create_task(_heartbeat())
                    if body is not None and body != "":
                        _stream_chunk(body)
                        last_emit[0] = time.monotonic()
                    continue
                _send_head(200, {"Content-Type": "text/plain; charset=utf-8"})
                hb_task = asyncio.create_task(_heartbeat())
            _stream_chunk(item)
            last_emit[0] = time.monotonic()
        if not head_sent[0]:
            _send_head(200, {"Content-Type": "text/plain; charset=utf-8"})
        _write_frame({"type": "response_end"})
    except (BrokenPipeError, OSError):
        pass
    finally:
        if hb_task is not None:
            hb_task.cancel()
            try:
                await hb_task
            except (asyncio.CancelledError, Exception):
                pass


def _call_handler(event):
    """Invoke the user handler and return the raw return value (NOT
    normalised). Splits dispatch from normalisation so the caller can
    detect generators / async iterables before we collapse them into a
    single response."""
    if style == "asgi":
        return ("normal", asyncio.run(_call_asgi(handler, event)))
    if style == "wsgi":
        return ("wsgi-tuple", _call_wsgi(handler, event))

    # Lambda / plain style.
    try:
        result = handler(event, _Context(event))
    except TypeError as te:
        msg = str(te)
        if "positional argument" in msg or "takes" in msg or "missing" in msg:
            try:
                result = handler(event)
            except TypeError:
                req = _build_flask_request(event)
                if req is not None:
                    result = handler(req)
                else:
                    raise
        else:
            raise

    if inspect.iscoroutine(result):
        result = asyncio.run(result)

    return ("raw", result)


def _dispatch_and_emit(event, streaming_enabled, keepalive_s):
    """End-to-end dispatch path: invoke the handler and either emit a
    streaming protocol exchange (response_start + chunks + response_end)
    or a single response frame, depending on the handler's return value.

    Returns nothing — frames go straight out via _write_frame.
    """
    kind, result = _call_handler(event)
    if kind == "normal":
        # ASGI tuple (status, headers, body)
        status, headers, body = result
        _write_frame({"type": "response", "statusCode": status, "headers": headers, "body": body})
        return
    if kind == "wsgi-tuple":
        status, headers, body = result
        _write_frame({"type": "response", "statusCode": status, "headers": headers, "body": body})
        return

    # Streaming detection. Async generators take precedence because
    # inspect.isgenerator is False for them.
    if inspect.isasyncgen(result):
        asyncio.run(_stream_async_iterable(result, streaming_enabled, keepalive_s))
        return
    if inspect.isgenerator(result):
        _stream_iterable(result, streaming_enabled, keepalive_s)
        return

    status, headers, body = _normalise_response(result)
    _write_frame({"type": "response", "statusCode": status, "headers": headers, "body": body})


# ── Main loop ──────────────────────────────────────────────────────────

max_reqs = int(os.environ.get("ORVA_MAX_REQUESTS", "0") or 0)
served = 0

try:
    while True:
        frame = _read_frame()
        if frame is None:
            sys.exit(0)  # stdin EOF
        ftype = frame.get("type")
        if ftype == "quit":
            _write_frame({"type": "bye"})
            sys.exit(0)
        if ftype != "request":
            continue

        event = frame.get("event") or {"method": "POST", "path": "/", "headers": {}, "body": ""}
        # Propagate call depth into the env so orva.invoke()'s SDK can
        # forward it on outbound nested calls. Without this each recursion
        # level would see depth="" and the host's depth guard never trips.
        _hdrs = (event.get("headers") or {}) if isinstance(event, dict) else {}
        _depth = _hdrs.get("x-orva-call-depth") or _hdrs.get("X-Orva-Call-Depth") or ""
        if _depth:
            os.environ["ORVA_CALL_DEPTH"] = _depth
        else:
            os.environ.pop("ORVA_CALL_DEPTH", None)

        # v0.4 C1: streaming flag + heartbeat interval ride on per-request
        # headers so the proxy can flip them at runtime without redeploying
        # the worker. Defaults match the system_config seed values.
        _streaming_on = (_hdrs.get("x-orva-streaming-enabled") or "1") != "0"
        try:
            _keepalive = max(1, int(_hdrs.get("x-orva-stream-keepalive-seconds") or "15"))
        except (TypeError, ValueError):
            _keepalive = 15

        try:
            _dispatch_and_emit(event, _streaming_on, _keepalive)
        except Exception:
            traceback.print_exc()
            # If we already started streaming we can't undo the head; emit
            # response_end and let the proxy close out. Otherwise emit a
            # plain 500 response.
            try:
                _write_frame({
                    "type": "response", "statusCode": 500,
                    "headers": {"Content-Type": "application/json"},
                    "body": json.dumps({"error": "Internal function error"}),
                })
            except Exception:
                # Best-effort terminator — the foreground proxy frame loop
                # will see EOF on the next read if even this fails.
                try:
                    _write_frame({"type": "response_end"})
                except Exception:
                    pass

        served += 1
        if max_reqs > 0 and served >= max_reqs:
            _write_frame({"type": "bye"})
            sys.exit(0)
except SystemExit:
    raise
except Exception as exc:
    try:
        _write_frame({"type": "error", "fatal": True, "message": f"{type(exc).__name__}: {exc}"})
    except Exception:
        pass
    sys.exit(1)
