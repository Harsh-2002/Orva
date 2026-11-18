"""SDK channel tests for backend/runtimes/python/orva.py.

Run: python3 -m unittest discover -s backend/runtimes/test -p 'test_*.py'
The parent plays the host over a real socketpair passed to the child as an fd.
"""
import base64
import http.server
import json
import os
import socket
import struct
import subprocess
import sys
import threading
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
CHILD = os.path.join(HERE, "python_child.py")


def send(sock, obj):
    body = json.dumps(obj).encode()
    sock.sendall(struct.pack(">I", len(body)) + body)


class Ctx:
    def __init__(self, sock):
        self.sock = sock
        self.frames = []
        self.lock = threading.Lock()

    def reply(self, i, r):
        send(self.sock, {"id": i, "ok": True, "r": r})

    def fail(self, i, status, code, message):
        send(self.sock, {"id": i, "ok": False, "e": {"status": status, "code": code, "message": message}})


def run(scenario, host, env=None, channel=True):
    """Run a scenario in a child; return (output, frames)."""
    parent, child_sock = socket.socketpair()
    e = {k: v for k, v in os.environ.items() if not k.startswith("ORVA_")}
    pass_fds = ()
    if channel:
        e.update(ORVA_SDK_FD=str(child_sock.fileno()), ORVA_SDK_PROTO="1")
        pass_fds = (child_sock.fileno(),)
    e.update(env or {})
    p = subprocess.Popen([sys.executable, CHILD, scenario], env=e, stdout=subprocess.PIPE,
                         pass_fds=pass_fds)
    child_sock.close()
    ctx = Ctx(parent)

    def serve():
        buf = b""
        try:
            while True:
                while len(buf) < 4 or len(buf) < 4 + struct.unpack(">I", buf[:4])[0]:
                    chunk = parent.recv(65536)
                    if not chunk:
                        return
                    buf += chunk
                n = struct.unpack(">I", buf[:4])[0]
                msg = json.loads(buf[4:4 + n])
                buf = buf[4 + n:]
                with ctx.lock:
                    ctx.frames.append(msg)
                host(msg, ctx)
        except OSError:
            pass

    t = threading.Thread(target=serve, daemon=True)
    t.start()
    try:
        out, _ = p.communicate(timeout=30)
    finally:
        if p.poll() is None:
            p.kill()
        parent.close()
    t.join(5)
    return json.loads(out), ctx.frames


def kv_host():
    store = {}
    lock = threading.Lock()

    def host(msg, ctx):
        a = msg.get("a", {})
        op = msg["op"]
        with lock:
            if op == "kv.get":
                if a["key"] not in store:
                    return ctx.fail(msg["id"], 404, "NOT_FOUND", "no such key")
                return ctx.reply(msg["id"], {"value": store[a["key"]]})
            if op == "kv.put":
                store[a["key"]] = a["value"]
                return ctx.reply(msg["id"], {})
            if op == "kv.delete":
                if store.pop(a["key"], None) is None:
                    return ctx.fail(msg["id"], 404, "NOT_FOUND", "no such key")
                return ctx.reply(msg["id"], {})
            if op == "kv.list":
                return ctx.reply(msg["id"], {"keys": [{"key": k} for k in store], "next_cursor": "after:" + a.get("cursor", "")})
            if op == "kv.batch":
                res = []
                for o in a["ops"]:
                    if o["op"] == "put":
                        store[o["key"]] = o["value"]
                        res.append({"op": "put", "key": o["key"], "found": True})
                    elif o["op"] == "delete":
                        res.append({"op": "delete", "key": o["key"], "found": store.pop(o["key"], None) is not None})
                    else:
                        res.append({"op": "get", "key": o["key"], "found": o["key"] in store, "value": store.get(o["key"])})
                return ctx.reply(msg["id"], {"results": res})
            if op == "kv.incr":
                store[a["key"]] = store.get(a["key"], 0) + a["delta"]
                return ctx.reply(msg["id"], {"value": store[a["key"]]})
            if op == "kv.cas":
                cur = store.get(a["key"])
                if cur != a["expected"]:
                    return ctx.reply(msg["id"], {"ok": False, "current": cur})
                store[a["key"]] = a["new"]
                return ctx.reply(msg["id"], {"ok": True})
        ctx.fail(msg["id"], 400, "UNKNOWN_OP", op)

    return host


class ChannelTests(unittest.TestCase):
    def test_concurrent_initialization_waits_for_channel_publication(self):
        out, frames = run("concurrent_initialization", kv_host())
        self.assertEqual(out["observed"], "lock")
        self.assertEqual(out["results"], {"first": {"ok": 1}, "second": {"ok": 1}})
        self.assertEqual(len(frames), 2)

    def test_round_trips_without_identity(self):
        out, frames = run("roundtrip", kv_host())
        self.assertEqual(out["get"], {"n": 1})
        self.assertEqual(out["miss"], "dflt")
        self.assertEqual(out["list"], {"keys": [{"key": "a"}, {"key": "b"}], "next_cursor": "after:c0"})
        self.assertEqual(out["many"], {"a": {"n": 1}, "zzz": None})
        self.assertEqual(out["del_many"], 2)
        self.assertEqual(out["incr"], 3)
        self.assertTrue(out["cas"])
        self.assertEqual(
            sorted({f["op"] for f in frames}),
            ["kv.batch", "kv.cas", "kv.delete", "kv.get", "kv.incr", "kv.list", "kv.put"],
        )
        put = next(f for f in frames if f["op"] == "kv.put")
        self.assertEqual(put["a"], {"key": "a", "value": {"n": 1}, "ttl_seconds": 5})
        for f in frames:
            self.assertEqual(set(f) - {"id", "op", "a"}, set())

    def test_error_mapping(self):
        def host(msg, ctx):
            k = msg["a"].get("key")
            if k == "busy":
                return ctx.fail(msg["id"], 429, "TOO_MANY_INFLIGHT", "slow down")
            if k == "bad":
                return ctx.fail(msg["id"], 400, "VALIDATION", "bad delta")
            if msg["op"] == "kv.cas":
                return ctx.reply(msg["id"], {"ok": False, "current": "cur"})
            if msg["op"] == "kv.batch":
                return ctx.reply(msg["id"], {"results": [{"key": "k", "error": "boom"}]})
            ctx.fail(msg["id"], 500, "X", "x")

        out, _ = run("errors", host)
        self.assertEqual(out["get_busy"]["err"]["name"], "OrvaError")
        self.assertEqual(out["get_busy"]["err"]["status"], 429)
        self.assertEqual(out["put_busy"]["err"]["status"], 429)
        self.assertEqual(out["incr_bad"]["err"]["status"], 400)
        self.assertEqual(out["mismatch"]["err"]["name"], "OrvaCASMismatch")
        self.assertEqual(out["mismatch"]["err"]["status"], 409)
        self.assertEqual(out["mismatch"]["err"]["current"], "cur")
        self.assertEqual(out["batch_err"]["err"]["status"], 500)

    def test_many_threads_out_of_order_and_at_most_32_outstanding(self):
        state = {"out": 0, "max": 0}
        held = []
        lock = threading.Lock()

        def flush(ctx):
            with lock:
                batch, held[:] = held[::-1], []
                state["out"] -= len(batch)
            for m in batch:
                ctx.reply(m["id"], {"value": m["a"]["delta"] * 10})

        def host(msg, ctx):
            with lock:
                state["out"] += 1
                state["max"] = max(state["max"], state["out"])
                held.append(msg)
                n = len(held)
            if n >= 7:
                flush(ctx)
            else:
                threading.Timer(0.01, flush, args=(ctx,)).start()

        out, frames = run("threads", host)
        self.assertEqual(len(frames), 64 * 5)
        self.assertLessEqual(state["max"], 32)
        for i in range(64):
            self.assertEqual(out["res"][str(i)], [j * 10 for j in range(5)])

    def test_more_than_32_are_queued_not_rejected(self):
        held = []
        state = {"released": False, "seen": 0}
        lock = threading.Lock()

        def host(msg, ctx):
            with lock:
                if state["released"]:
                    return ctx.reply(msg["id"], {"value": 1})
                held.append(msg)
                if len(held) != 32:
                    return

            def release():
                with lock:
                    state["seen"] = len(ctx.frames)
                    state["released"] = True
                    batch = list(held)
                for m in batch:
                    ctx.reply(m["id"], {"value": 1})

            threading.Timer(0.2, release).start()

        out, frames = run("threads", host)
        self.assertEqual(state["seen"], 32)
        self.assertEqual(len(frames), 64 * 5)

    def test_eof_rejects_pending_and_later_calls(self):
        def host(msg, ctx):
            if msg["a"]["key"] == "x":
                ctx.sock.shutdown(socket.SHUT_RDWR)

        out, _ = run("eof", host)
        self.assertEqual(out["first"]["err"]["name"], "OrvaUnavailableError")
        self.assertEqual(out["second"]["err"]["name"], "OrvaUnavailableError")

    def test_oversized_request_refused_before_write(self):
        out, frames = run("big", lambda m, c: None)
        self.assertEqual(out["err"]["name"], "OrvaError")
        self.assertIn("too large", out["err"]["message"])
        self.assertEqual(frames, [])

    def test_nan_is_refused_client_side_and_does_not_reach_the_host(self):
        out, frames = run("nan", lambda m, c: None)
        self.assertEqual(out["err"]["name"], "OrvaError")
        self.assertEqual(out["err"].get("status"), 400)
        self.assertEqual(frames, [])

    def test_reply_length_over_4mib_closes_channel(self):
        def host(msg, ctx):
            ctx.sock.sendall(struct.pack(">I", (4 << 20) + 1))

        out, _ = run("badlen", host)
        self.assertEqual(out["err"]["name"], "OrvaUnavailableError")

    def test_timeout_without_reply(self):
        out, _ = run("timeout", lambda m, c: None)
        self.assertEqual(out["err"]["name"], "OrvaError")
        self.assertEqual(out["err"]["status"], 0)
        self.assertIn("timed out", out["err"]["message"])

    def test_lease_absent_then_attached(self):
        _, frames = run("lease", kv_host())
        self.assertEqual([f.get("lease") for f in frames], [None, "L-1", "L-2"])

    def test_fork_child_refuses_parent_still_works(self):
        out, frames = run("fork", kv_host())
        self.assertEqual(out["child"]["err"]["name"], "OrvaUnavailableError")
        self.assertEqual([f["a"]["key"] for f in frames], ["parent-before", "parent-after"])

    def test_fork_before_first_sdk_call_refuses_child(self):
        out, frames = run("fork_lazy", kv_host())
        self.assertEqual(out["child"]["err"]["name"], "OrvaUnavailableError")
        self.assertEqual([f["a"]["key"] for f in frames], ["parent-after"])


class _Handler(http.server.BaseHTTPRequestHandler):
    seen = []

    def _do(self):
        n = int(self.headers.get("content-length") or 0)
        self.rfile.read(n)
        type(self).seen.append(f"{self.command} {self.path}")
        body = b"{}"
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_GET = do_PUT = do_POST = do_DELETE = _do

    def log_message(self, *a):
        pass


class UnavailableTests(unittest.TestCase):
    def serve(self):
        _Handler.seen = []
        srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        return srv.server_address[1]

    def env(self, port):
        return {"ORVA_API_BASE": f"http://127.0.0.1:{port}", "ORVA_INTERNAL_TOKEN": "tok", "ORVA_FUNCTION_ID": "f1"}

    def test_all_operations_fail_closed_without_http(self):
        port = self.serve()
        for name, env in [
            ("SDK permission disabled", {}),
            ("protocol missing", {"ORVA_SDK_FD": "3"}),
            ("protocol unsupported", {"ORVA_SDK_FD": "3", "ORVA_SDK_PROTO": "2"}),
            ("descriptor missing", {"ORVA_SDK_PROTO": "1"}),
            ("descriptor invalid", {"ORVA_SDK_FD": "bad", "ORVA_SDK_PROTO": "1"}),
            ("descriptor negative", {"ORVA_SDK_FD": "-1", "ORVA_SDK_PROTO": "1"}),
            ("descriptor is standard I/O", {"ORVA_SDK_FD": "1", "ORVA_SDK_PROTO": "1"}),
            ("descriptor not a socket", {"ORVA_TEST_NONSOCKET": "1", "ORVA_SDK_PROTO": "1"}),
            ("descriptor closed", {"ORVA_SDK_FD": "999999", "ORVA_SDK_PROTO": "1"}),
            ("descriptor out of range", {"ORVA_SDK_FD": "9" * 100, "ORVA_SDK_PROTO": "1"}),
            ("descriptor non-ASCII", {"ORVA_SDK_FD": "²", "ORVA_SDK_PROTO": "1"}),
        ]:
            with self.subTest(name=name):
                out, frames = run("unavailable", lambda m, c: self.fail("channel used"),
                                  env={**self.env(port), **env}, channel=False)
                self.assertEqual(len(out["errors"]), 13)
                for op, result in out["errors"].items():
                    self.assertEqual(result.get("err", {}).get("name"), "OrvaUnavailableError", op)
                    self.assertEqual(result["err"]["status"], 0, op)
                    self.assertRegex(result["err"]["message"], r"sdk permission.*refresh.*adapter", op)
                self.assertTrue(out["span_ran"])
                self.assertEqual(out["span_error"]["err"]["message"], "user failure")
                self.assertEqual(frames, [])
                self.assertEqual(_Handler.seen, [])

    def test_explicit_test_hook_and_reset(self):
        out, frames = run("test_hook", lambda m, c: self.fail("channel used"),
                          env={"ORVA_FUNCTION_ID": "f1"}, channel=False)
        self.assertEqual(out["value"], "mock")
        self.assertEqual(out["seen"], [{"method": "GET", "path": "/api/v1/_kv/f1/a%2Fb"}])
        self.assertEqual(out["restored"]["err"]["name"], "OrvaUnavailableError")
        self.assertEqual(frames, [])


IDENT_ENV = {"ORVA_TRACE_ID": "tr1", "ORVA_SPAN_ID": "sp1", "ORVA_EXECUTION_ID": "ex1",
             "ORVA_CALL_DEPTH": "2", "ORVA_FUNCTION_ID": "f1"}


def b64(i):
    return base64.b64encode(f"c{i}".encode()).decode()


def ops_host(state):
    state.setdefault("handles", {})
    state.setdefault("closed", [])
    state["n"] = 0

    def host(msg, ctx):
        a = msg.get("a", {})
        op = msg["op"]
        i = msg["id"]
        if op == "jobs.enqueue":
            if a["function_name"] == "bad":
                return ctx.fail(i, 400, "VALIDATION", "bad job")
            return ctx.reply(i, {"id": "job9", "replayed": a.get("idempotency_key") == "k"})
        if op == "crons.upsert":
            if a["schedule"] == "bad":
                return ctx.fail(i, 403, "SDK_SCOPE_VIOLATION", "no")
            return ctx.reply(i, {"id": "cron1", "name": a["name"]})
        if op == "spans.emit":
            if a["name"] == "s":
                return ctx.fail(i, 403, "SDK_SCOPE_VIOLATION", "no")
            return ctx.reply(i, {})
        if op == "invoke":
            n = a["name"]
            if n == "missing":
                return ctx.fail(i, 404, "NOT_FOUND", "function not found")
            if n == "deep":
                return ctx.fail(i, 507, "MAX_CALL_DEPTH", "too deep")
            if n == "busy":
                return ctx.fail(i, 429, "FUNCTION_BUSY", "busy")
            if n == "plain":
                return ctx.reply(i, {"statusCode": 200, "headers": {}, "body": "not json"})
            return ctx.reply(i, {"statusCode": 200, "headers": {"a": "b"}, "body": json.dumps({"echo": a["body"]["q"]})})
        if op == "invoke.stream.open":
            if a["name"] == "missing":
                return ctx.fail(i, 404, "NOT_FOUND", "function not found")
            state["n"] += 1
            h = f"h{state['n']}"
            state["handles"][h] = {"name": a["name"], "i": 0}
            return ctx.reply(i, {"handle": h, "status": 200, "headers": {}})
        if op == "invoke.stream.next":
            st = state["handles"].get(a["handle"])
            if st is None:
                return ctx.fail(i, 404, "NOT_FOUND", "no such handle")
            st["i"] += 1
            if st["name"] == "mid" and st["i"] == 3:
                del state["handles"][a["handle"]]
                return ctx.fail(i, 502, "INVOKE_FAILED", "callee died")
            if st["name"] == "five" and st["i"] == 3 and state.get("eof_at_three"):
                return ctx.sock.shutdown(socket.SHUT_RDWR)
            if st["i"] <= 5:
                return ctx.reply(i, {"chunk_b64": b64(st["i"]), "done": False})
            del state["handles"][a["handle"]]
            return ctx.reply(i, {"chunk_b64": "", "done": True})
        if op == "invoke.stream.close":
            state["closed"].append(a["handle"])
            state["handles"].pop(a["handle"], None)
            return ctx.reply(i, {})
        ctx.fail(i, 400, "UNKNOWN_OP", op)

    return host


class PR2OpsTests(unittest.TestCase):
    def assert_no_identity(self, frames):
        for f in frames:
            self.assertEqual(set(f) - {"id", "op", "a", "lease"}, set())
            for k in ("function_id", "execution_id", "trace_id", "span_id", "token", "depth", "call_depth", "headers"):
                self.assertNotIn(k, f["a"], f"{k} in {f['op']}")

    def test_jobs_crons_spans_invoke_over_channel(self):
        out, frames = run("ops_ok", ops_host({}), env=IDENT_ENV)
        self.assertEqual(out["job"], {"id": "job9", "replayed": True})
        self.assertEqual(out["cron"], {"id": "cron1", "name": "nightly"})
        self.assertEqual(out["inv"], {"statusCode": 200, "headers": {"a": "b"}, "body": {"echo": 1}})
        self.assertEqual(out["inv_plain"]["body"], "not json")
        self.assertEqual([f["op"] for f in frames], ["jobs.enqueue", "crons.upsert", "spans.emit", "invoke", "invoke"])
        self.assertEqual(frames[0]["a"], {"function_name": "fn2", "payload": {"a": 1}, "max_attempts": 5,
                                          "idempotency_key": "k", "idempotency_window_seconds": 60})
        self.assertEqual(frames[1]["a"], {"name": "nightly", "schedule": "0 3 * * *", "payload": {"x": 1},
                                          "timezone": "UTC", "enabled": True})
        self.assertEqual(frames[2]["a"]["name"], "work")
        self.assertEqual(frames[2]["a"]["status"], "ok")
        self.assertEqual(frames[2]["a"]["attributes"], {"a": 1})
        self.assertIsInstance(frames[2]["a"]["duration_ms"], int)
        self.assertEqual(frames[3]["a"], {"name": "callee", "body": {"q": 1}})
        self.assertEqual(frames[4]["a"], {"name": "plain", "body": {}})
        self.assertTrue(all(f.get("lease") == "L-1" for f in frames))
        self.assert_no_identity(frames)

    def test_op_error_mapping(self):
        out, _ = run("ops_errors", ops_host({}), env=IDENT_ENV)
        self.assertEqual(out["not_found"]["err"]["status"], 404)
        self.assertIn("function not found: missing", out["not_found"]["err"]["message"])
        self.assertEqual(out["depth"]["err"]["status"], 507)
        self.assertEqual(out["depth"]["err"]["message"], "call depth exceeded")
        self.assertEqual(out["busy"]["err"]["status"], 429)
        self.assertEqual(out["job"]["err"]["status"], 400)
        self.assertEqual(out["job"]["err"]["name"], "OrvaError")
        self.assertEqual(out["cron"]["err"]["status"], 403)
        self.assertEqual(out["span_swallowed"], "fine")

    def test_invoke_stream_chunks_early_close_mid_error(self):
        state = {}
        out, frames = run("streams", ops_host(state), env=IDENT_ENV)
        five = [f"c{i}|bytes" for i in range(1, 6)]
        self.assertEqual(out["full"], five)
        self.assertEqual(out["early"], five[:2])
        self.assertEqual(out["mid"], ["c1", "c2"])
        self.assertEqual(out["mid_err"]["err"]["name"], "OrvaError")
        self.assertEqual(out["mid_err"]["err"]["status"], 502)
        self.assertEqual(out["open_err"]["err"]["status"], 404)
        self.assertIn("function not found: missing", out["open_err"]["err"]["message"])
        # h1 finished on its own, h2 was closed by the early break, h3 failed (host already closed it).
        self.assertEqual(state["closed"], ["h2"])
        ops = [f["op"] + (":" + f["a"]["handle"] if "handle" in f["a"] else "") for f in frames]
        self.assertEqual(ops, (
            ["invoke.stream.open"] + ["invoke.stream.next:h1"] * 6
            + ["invoke.stream.open", "invoke.stream.next:h2", "invoke.stream.next:h2", "invoke.stream.close:h2"]
            + ["invoke.stream.open"] + ["invoke.stream.next:h3"] * 3
            + ["invoke.stream.open"]
        ))
        self.assertEqual(frames[0]["a"], {"name": "five", "body": {"p": 1}})
        self.assertEqual(frames[1]["a"], {"handle": "h1"})
        self.assert_no_identity(frames)

    def test_channel_eof_during_stream(self):
        out, _ = run("stream_eof", ops_host({"eof_at_three": True}), env=IDENT_ENV)
        self.assertEqual(out["got"], ["c1", "c2"])
        self.assertEqual(out["res"]["err"]["name"], "OrvaUnavailableError")


class MarkerTest(unittest.TestCase):
    def test_adapters_carry_sdk_proto_marker(self):
        for f in ("node/adapter.js", "python/adapter.py"):
            with open(os.path.join(HERE, "..", f), encoding="utf-8") as fh:
                head = "".join(fh.readlines()[:40])
            self.assertIn("orva-sdk-proto:1", head, f)


if __name__ == "__main__":
    unittest.main()
