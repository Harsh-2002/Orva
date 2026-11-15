"""Child half of test_python_sdk_channel.py: runs one scenario, prints JSON."""
import json
import os
import sys
import threading

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


def http():
    kv.put("k", "v")
    v = kv.get("k")
    job = orva.jobs.enqueue("fn2", {"a": 1})
    return {"v": v, "job": job}


def fork():
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


print(json.dumps(globals()[sys.argv[1]](), default=repr))
