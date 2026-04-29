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
import importlib.util
import inspect
import json
import os
import sys
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


def _write_frame(obj):
    body = json.dumps(obj).encode("utf-8")
    _stdout.write(struct.pack(">I", len(body)))
    _stdout.write(body)
    _stdout.flush()


def _dispatch(event):
    if style == "asgi":
        status, headers, body = asyncio.run(_call_asgi(handler, event))
        return status, headers, body
    if style == "wsgi":
        return _call_wsgi(handler, event)

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

    return _normalise_response(result)


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
        try:
            status, headers, body = _dispatch(event)
        except Exception:
            traceback.print_exc()
            status, headers, body = (
                500,
                {"Content-Type": "application/json"},
                json.dumps({"error": "Internal function error"}),
            )
        _write_frame({"type": "response", "statusCode": status, "headers": headers, "body": body})

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
