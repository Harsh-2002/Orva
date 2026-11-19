"""Child half of test_python_sdk_channel.py: runs one scenario, prints JSON."""
import json
import os
import sys
import threading
import time

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "python"))
import orva  # noqa: E402

kv = orva.kv


def attempt(fn):
    try:
        return {"ok": fn()}
    except Exception as e:  # noqa: BLE001
        return {"err": {"name": type(e).__name__, "status": getattr(e, "status", None),
                        "message": str(e), "current": getattr(e, "current_value", None)}}


def roundtrip():
    kv.put("a", {"n": 1}, ttl_seconds=5)
    kv.put("b", "two")
    return {
        "get": kv.get("a"),
        "miss": kv.get("nope", "dflt"),
        "list": kv.list(prefix="", limit=10, cursor="c0"),
        "many": kv.get_many(["a", "zzz"]),
        "put_many": kv.put_many([{"key": "x", "value": 1, "ttl_seconds": 1}, {"key": "y", "value": 2}]),
        "del_many": kv.delete_many(["x", "y", "q"]),
        "incr": kv.incr("ctr", 3),
        "cas": kv.cas("a", {"n": 1}, {"n": 2}),
        "del": kv.delete("b"),
        "del_missing": kv.delete("gone"),
    }


def errors():
    return {
        "get_busy": attempt(lambda: kv.get("busy")),
        "put_busy": attempt(lambda: kv.put("busy", 1)),
        "incr_bad": attempt(lambda: kv.incr("bad")),
        "mismatch": attempt(lambda: kv.cas("m", 1, 2)),
        "batch_err": attempt(lambda: kv.put_many([{"key": "k", "value": 1}])),
    }


def threads():
    out = {}

    def work(i):
        out[i] = [kv.incr(f"k{i}", j) for j in range(5)]

    ts = [threading.Thread(target=work, args=(i,)) for i in range(64)]
    for t in ts:
        t.start()
    for t in ts:
        t.join()
    return {"res": {str(k): v for k, v in out.items()}}


def concurrent_initialization():
    import queue

    entered = threading.Event()
    release = threading.Event()
    observations = queue.Queue()
    original_chan = orva._Chan
    original_lock = orva._chan_lock

    def paused_chan(fd):
        entered.set()
        if not release.wait(5):
            raise RuntimeError("initialization barrier timed out")
        return original_chan(fd)

    class ObservedLock:
        def __enter__(self):
            if threading.current_thread().name == "second":
                observations.put("lock")
            original_lock.acquire()

        def __exit__(self, *_):
            original_lock.release()

    orva._Chan = paused_chan
    orva._chan_lock = ObservedLock()
    results = {}

    def work(name):
        results[name] = attempt(lambda: kv.incr(name))
        if name == "second":
            observations.put("returned")

    first = threading.Thread(target=work, args=("first",), name="first")
    second = threading.Thread(target=work, args=("second",), name="second")
    first.start()
    try:
        if not entered.wait(5):
            raise RuntimeError("initializer did not enter")
        second.start()
        observed = observations.get(timeout=5)
    finally:
        release.set()
        first.join(5)
        if second.ident is not None:
            second.join(5)
    if first.is_alive() or second.is_alive():
        raise RuntimeError("initialization callers did not finish")
    return {"observed": observed, "results": results}


def eof():
    return {"first": attempt(lambda: kv.get("x")), "second": attempt(lambda: kv.get("y"))}


def big():
    return attempt(lambda: kv.put("k", "x" * (5 << 20)))


def nan():
    return attempt(lambda: kv.put("k", float("nan")))


def badlen():
    return attempt(lambda: kv.get("k"))


def lease():
    kv.get("before")
    orva._set_lease("L-1")
    kv.get("after")
    orva._set_lease("L-2")
    kv.get("after2")
    return {}


def timeout():
    orva._set_channel_timeout(0.3)
    return attempt(lambda: kv.get("slow"))


def unavailable():
    if os.environ.get("ORVA_TEST_NONSOCKET"):
        os.environ["ORVA_SDK_FD"] = str(os.open("/dev/null", os.O_RDONLY))
    calls = {
        "get": lambda: kv.get("k"),
        "put": lambda: kv.put("k", "v"),
        "delete": lambda: kv.delete("k"),
        "list": lambda: kv.list(),
        "get_many": lambda: kv.get_many(["k"]),
        "put_many": lambda: kv.put_many([{"key": "k", "value": 1}]),
        "delete_many": lambda: kv.delete_many(["k"]),
        "incr": lambda: kv.incr("k"),
        "cas": lambda: kv.cas("k", None, 1),
        "invoke": lambda: orva.invoke("callee"),
        "stream": lambda: list(orva.invoke_stream("callee")),
        "job": lambda: orva.jobs.enqueue("fn2"),
        "cron": lambda: orva.crons.upsert("nightly", "0 3 * * *"),
    }
    errors = {name: attempt(fn) for name, fn in calls.items()}
    span_ran = False
    with orva.trace.span("work"):
        span_ran = True

    def fail_span():
        with orva.trace.span("error"):
            raise ValueError("user failure")

    return {"errors": errors, "span_ran": span_ran, "span_error": attempt(fail_span)}


def test_hook():
    seen = []

    def request(method, path, opts):
        seen.append({"method": method, "path": path})
        return 200, b'{"value":"mock"}'

    orva.__test_mode__({"request": request})
    value = kv.get("a/b")
    orva.__test_mode__(None)
    return {"value": value, "seen": seen, "restored": attempt(lambda: kv.get("k"))}


def ops_ok():
    orva._set_lease("L-1")
    job = orva.jobs.enqueue("fn2", {"a": 1}, max_attempts=5, idempotency_key="k", idempotency_window_seconds=60)
    cron = orva.crons.upsert("nightly", "0 3 * * *", payload={"x": 1}, timezone="UTC", enabled=True)
    with orva.trace.span("work", {"a": 1}):
        pass
    inv = orva.invoke("callee", {"q": 1})
    inv_plain = orva.invoke("plain")
    return {"job": job, "cron": cron, "inv": inv, "inv_plain": inv_plain}


def ops_errors():
    orva._set_lease("L-1")

    def span_swallowed():
        with orva.trace.span("s"):
            return "fine"

    return {
        "not_found": attempt(lambda: orva.invoke("missing")),
        "depth": attempt(lambda: orva.invoke("deep")),
        "busy": attempt(lambda: orva.invoke("busy")),
        "job": attempt(lambda: orva.jobs.enqueue("bad")),
        "cron": attempt(lambda: orva.crons.upsert("x", "bad")),
        "span_swallowed": span_swallowed(),
    }


def streams():
    orva._set_lease("L-1")
    full = [c.decode() + "|" + type(c).__name__ for c in orva.invoke_stream("five", {"p": 1})]
    early = []
    gen = orva.invoke_stream("five")
    for c in gen:
        early.append(c.decode() + "|" + type(c).__name__)
        if len(early) == 2:
            break
    gen.close()  # early break: GeneratorExit must close the handle
    mid = []

    def run_mid():
        for c in orva.invoke_stream("mid"):
            mid.append(c.decode())

    mid_err = attempt(run_mid)

    def run_missing():
        for _ in orva.invoke_stream("missing"):
            pass

    return {"full": full, "early": early, "mid": mid, "mid_err": mid_err, "open_err": attempt(run_missing)}


def stream_eof():
    orva._set_lease("L-1")
    got = []

    def run_it():
        for c in orva.invoke_stream("five"):
            got.append(c.decode())

    return {"got": got, "res": attempt(run_it)}


def fork(warm=True):
    if warm:
        kv.get("parent-before")
    r, w = os.pipe()
    pid = os.fork()
    if pid == 0:
        os.close(r)
        res = attempt(lambda: kv.get("child"))
        os.write(w, json.dumps(res).encode())
        os._exit(0)
    os.close(w)
    child = json.loads(os.read(r, 65536).decode())
    os.waitpid(pid, 0)
    return {"child": child, "parent_after": kv.get("parent-after")}


def fork_lazy():
    return fork(warm=False)


print(json.dumps(globals()[sys.argv[1]](), default=repr))
