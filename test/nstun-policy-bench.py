#!/usr/bin/env python3
"""nstun-policy-bench.py — BENCHMARK, not a correctness test.

Measures two properties of the per-sandbox NSTUN egress policy that the
correctness suites deliberately do not cover, because both are about cost and
saturation rather than about right/wrong answers:

  Q1  Per-connection policy cost. NSTUN evaluates its rule vector linearly,
      first-match-wins, on every new flow (for TCP, on the SYN) — where the old
      host-nftables implementation used an O(1) set lookup. This sweeps the
      blocklist across several sizes and measures connect() latency from inside
      an egress sandbox. Two independent estimators:
        * allow path — the probe destination matches nothing, so every rule is
          walked before NSTUN's default-ALLOW applies (worst case).
        * reject depth — inside ONE policy, compare a destination matched by the
          FIRST reject rule against one matched by the LAST. Same worker, same
          policy, same everything: only the walk depth differs.

  Q2  The flow cap. nsjail's nstun holds at most 1024 TCP flows per jail and
      checks that cap BEFORE policy evaluation, so at saturation a REJECT rule
      stops emitting RSTs and the SYN is silently dropped instead. This drives a
      sandbox to saturation and reports what the guest actually observes for a
      blocked destination, for a fresh allowed one, and how long recovery takes.

This script is NOT part of run-all.sh and must not be added to it: it creates up
to a thousand blocklist rules, republishes the egress policy on every one of
them, and repeatedly retires the warm pool. Run it deliberately, against an
instance you own.

Requirements
  * Must run on the SAME HOST as orvad. It binds its probe listeners to the
    daemon's own control-plane address (the address sandboxes use to reach
    orvad) because that is the one address a sandbox is guaranteed to be able to
    route to. A remote runner cannot offer such a target.
  * nsjail present and egress functional (the same bar as egress-test.sh).

Usage
  BASE_URL=http://localhost:18443 API_KEY=orva_... ./test/nstun-policy-bench.py

Environment
  BASE_URL          default http://localhost:18443
  API_KEY           required
  BENCH_RULE_STEPS  default "0,50,200,500,1000"  blocklist sizes to sweep
  BENCH_ROUNDS      default 3    passes over the ladder (medians are per-level)
  BENCH_SAMPLES     default 400  connects per measurement invocation
  BENCH_SKIP_FLOWCAP set to 1 to run Q1 only (Q2 takes ~2 minutes)

Everything it creates — the probe function and every rule it adds — is removed
on every exit path, including SIGINT/SIGTERM.
"""

import atexit
import json
import os
import resource
import signal
import socket
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request

BASE = os.environ.get("BASE_URL", "http://localhost:18443").rstrip("/")
KEY = os.environ.get("API_KEY", "")
STEPS = [int(x) for x in os.environ.get("BENCH_RULE_STEPS", "0,50,200,500,1000").split(",")]
ROUNDS = int(os.environ.get("BENCH_ROUNDS", "3"))
SAMPLES = int(os.environ.get("BENCH_SAMPLES", "400"))
SKIP_FLOWCAP = os.environ.get("BENCH_SKIP_FLOWCAP", "") == "1"

# Label stamped on every rule this run creates. The prefix is what cleanup
# matches, so a rule left behind by an interrupted earlier run is swept too.
LABEL_PREFIX = "nstun-bench"
LABEL = "%s-%d" % (LABEL_PREFIX, os.getpid())

# Synthetic blocklist entries come from 198.18.0.0/15, the RFC 2544 benchmarking
# range: never routed on the public internet, and 131072 addresses deep, so the
# ladder can grow without colliding with anything real.
def cidr(i):
    return "198.18.%d.%d/32" % (i // 256, i % 256)


# 169.254.0.0/16 ships enabled as a 'default' rule and sorts ahead of every
# 198.18.* entry, so it is the FIRST reject in every policy this script builds —
# the shallow end of the reject-depth estimator.
FIRST_REJECT_IP = "169.254.169.254"


def last_reject_ip(n):
    """Destination matched by the LAST reject rule at ladder step n."""
    return sorted([cidr(i) for i in range(n)])[-1].split("/")[0] if n else None


# ─────────────────────────────── HTTP ────────────────────────────────


def api(method, path, body=None, timeout=120):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        BASE + path, data=data, method=method,
        headers={"X-Orva-API-Key": KEY, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read() or b"null")


def invoke(body, timeout=120):
    req = urllib.request.Request(
        BASE + "/fn/" + FN["id"] + "/", data=json.dumps(body).encode(), method="POST",
        headers={"X-Orva-API-Key": KEY, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read())


# ──────────────────────────── probe targets ──────────────────────────
#
# Two listeners, because who closes the connection first decides whether nstun
# frees the flow: SINK closes as soon as it accepts (server-closed), HOLD never
# closes (guest-closed). Q1 measures against SINK so the flow map stays empty;
# Q2 saturates against HOLD.


def _sink(sock):
    while True:
        try:
            c, _ = sock.accept()
        except OSError:
            return
        c.close()


def _hold(sock, kept):
    while True:
        try:
            c, _ = sock.accept()
        except OSError:
            # Never give up the accept loop: a listener that stops accepting
            # would stall the guest's connects and be indistinguishable from the
            # flow-cap saturation this script is trying to detect.
            time.sleep(0.05)
            continue
        kept.append(c)


def listen_on(addr, handler, held=None):
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((addr, 0))
    s.listen(4096)
    args = (s,) if held is None else (s, held)
    threading.Thread(target=handler, args=args, daemon=True).start()
    return s, s.getsockname()[1]


# ───────────────────────────── the probe fn ──────────────────────────

HANDLER = r"""
const net = require('net');
const fs = require('fs');

function conn(host, port, tmo, keep) {
  return new Promise((res) => {
    const t0 = process.hrtime.bigint();
    let done = false;
    const s = new net.Socket();
    const fin = (o, c) => {
      if (done) return; done = true;
      const ms = Number(process.hrtime.bigint() - t0) / 1e6;
      if (!keep || o !== 'connected') { try { s.destroy(); } catch (e) {} }
      res({ o, c: c || null, ms, s: keep && o === 'connected' ? s : null });
    };
    s.setTimeout(tmo, () => fin('timeout', 'TIMEOUT'));
    s.once('error', (e) => fin('error', String(e.code || e.message)));
    s.once('connect', () => fin('connected', null));
    s.connect({ host, port });
  });
}

function stats(a) {
  if (!a.length) return null;
  const s = a.slice().sort((x, y) => x - y);
  const q = (p) => s[Math.min(s.length - 1, Math.round(p * (s.length - 1)))];
  const r = (v) => Math.round(v * 1000) / 1000;
  return { n: s.length, min: r(s[0]), p50: r(q(0.5)), p90: r(q(0.9)),
           p95: r(q(0.95)), p99: r(q(0.99)), max: r(s[s.length - 1]) };
}

exports.handler = async (event) => {
  let b = {};
  try { b = JSON.parse(event.body || '{}'); } catch (e) {}
  const out = { mode: b.mode };
  const J = (o) => ({ statusCode: 200, headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify(o) });

  // How many concurrent sockets can this sandbox actually hold? nsjail's
  // default RLIMIT_NOFILE decides, and it is the ceiling on concurrent flows a
  // single worker can reach through the socket API.
  if (b.mode === 'limits') {
    try {
      out.nofile = fs.readFileSync('/proc/self/limits', 'utf8')
        .split('\n').filter((l) => /open files/.test(l))[0].trim().replace(/\s+/g, ' ');
    } catch (e) { out.nofile = 'unreadable: ' + e; }
    const held = [];
    let stop = null;
    for (let i = 0; i < (b.probe_fds || 64); i++) {
      const r = await conn(b.host, b.port, 3000, true);
      if (!r.s) { stop = r.c || r.o; break; }
      held.push(r.s);
    }
    out.max_concurrent_sockets = held.length;
    out.stopped_because = stop;
    for (const s of held) { try { s.destroy(); } catch (e) {} }
    return J(out);
  }

  // Sequential connect()/close latency against one destination.
  if (b.mode === 'latency') {
    const warm = b.warm == null ? 20 : b.warm, n = b.n == null ? 200 : b.n;
    const ok = [], bad = [], errs = {};
    for (let i = 0; i < warm + n; i++) {
      const r = await conn(b.host, b.port, b.connect_timeout_ms || 5000, false);
      if (i < warm) continue;
      if (r.o === 'connected') ok.push(r.ms);
      else { bad.push(r.ms); errs[r.c] = (errs[r.c] || 0) + 1; }
    }
    out.connected = stats(ok);
    out.failed = stats(bad);
    out.errors = errs;
    return J(out);
  }

  // Drive the nstun flow map to saturation with sequential, guest-closed
  // connections (one fd at a time — the fd ceiling is not a defence), then
  // report what a blocked and a fresh allowed destination look like from the
  // guest while saturated, and how long recovery takes.
  if (b.mode === 'saturate') {
    const t0 = Date.now();
    out.before = {};
    let r = await conn(b.blocked_host, b.blocked_port, 2000, false);
    out.before.blocked = { o: r.o, c: r.c, ms: +r.ms.toFixed(1) };
    r = await conn(b.host, b.port, 2000, false);
    out.before.allowed = { o: r.o, c: r.c, ms: +r.ms.toFixed(1) };

    let i = 0, stalled = null;
    while (i < (b.max_attempts || 2000) && Date.now() - t0 < (b.budget_ms || 40000)) {
      const c = await conn(b.hold_host, b.hold_port, 2000, false);
      i++;
      if (c.o !== 'connected') { stalled = c.c; break; }
    }
    out.attempts_to_saturate = i;
    out.saturation_signal = stalled;
    out.saturate_ms = Date.now() - t0;

    out.at_cap = {};
    r = await conn(b.blocked_host, b.blocked_port, 2000, false);
    out.at_cap.blocked = { o: r.o, c: r.c, ms: +r.ms.toFixed(1) };
    r = await conn(b.host, b.port, 2000, false);
    out.at_cap.allowed = { o: r.o, c: r.c, ms: +r.ms.toFixed(1) };
    return J(out);
  }

  // Poll a blocked destination until it fast-fails again. It answers with
  // ECONNREFUSED below the cap and with nothing at all at the cap, so the flip
  // back is exactly the moment nstun reclaimed slots.
  if (b.mode === 'recover') {
    const t0 = Date.now();
    out.polls = [];
    for (let k = 0; k < (b.max_polls || 12); k++) {
      const r = await conn(b.blocked_host, b.blocked_port, 2000, false);
      out.polls.push({ t_s: Math.round((Date.now() - t0) / 1000), o: r.o, c: r.c });
      if (r.c === 'ECONNREFUSED') { out.recovered_after_s = Math.round((Date.now() - t0) / 1000); break; }
      await new Promise((res) => setTimeout(res, 3000));
    }
    return J(out);
  }

  return { statusCode: 400, headers: {}, body: '{"error":"unknown mode"}' };
};
"""

FN = {"id": None}
_mem = [256]


def drain_pool():
    """Retire the warm worker so the next invoke spawns under the policy that is
    in force right now. A policy-driven recycle is rate-limited to one per 60s;
    a PUT that touches the spawn config drains immediately, so the benchmark
    measures steady-state warm invocations of a KNOWN generation rather than
    whatever generation the previous pool happened to capture."""
    _mem[0] = 320 if _mem[0] == 256 else 256
    api("PUT", "/api/v1/functions/" + FN["id"], {"memory_mb": _mem[0]})
    time.sleep(2.0)


def live_worker_generations():
    """Policy generations of the nsjail workers running right now, read off
    their argv. The generation is in the --config path, so this confirms the
    policy under measurement is the policy actually loaded — /firewall/status
    alone only proves what was published."""
    gens = set()
    try:
        pids = [p for p in os.listdir("/proc") if p.isdigit()]
    except OSError:
        return gens
    for p in pids:
        try:
            argv = open("/proc/%s/cmdline" % p, "rb").read().split(b"\0")
        except OSError:
            continue
        if argv and argv[0].endswith(b"nsjail") and b"--config" in argv:
            path = argv[argv.index(b"--config") + 1].decode(errors="replace")
            gens.add(path.split("egress-")[-1].replace(".cfg", ""))
    return gens


# ────────────────────────────── rule ladder ──────────────────────────


def bench_rules():
    return {r["value"]: r["id"] for r in api("GET", "/api/v1/firewall/rules")["rules"]
            if (r.get("label") or "").startswith(LABEL_PREFIX)}


def set_rules(target):
    have = bench_rules()
    want = {cidr(i) for i in range(target)}
    for value, rid in have.items():
        if value not in want:
            api("DELETE", "/api/v1/firewall/rules/%d" % rid)
    for value in sorted(want - set(have)):
        try:
            api("POST", "/api/v1/firewall/rules",
                {"value": value, "rule_type": "cidr", "label": LABEL})
        except urllib.error.HTTPError as e:
            if e.code != 409:
                raise


# ─────────────────────────────── cleanup ─────────────────────────────

_cleaned = [False]


def cleanup():
    if _cleaned[0]:
        return
    _cleaned[0] = True
    try:
        for _, rid in bench_rules().items():
            try:
                api("DELETE", "/api/v1/firewall/rules/%d" % rid, timeout=30)
            except Exception:
                pass
    except Exception:
        pass
    if FN["id"]:
        try:
            api("DELETE", "/api/v1/functions/" + FN["id"], timeout=30)
        except Exception:
            pass
    print("cleanup: bench rules and probe function removed")


def _sig(signum, _frame):
    cleanup()
    sys.exit(128 + signum)


# ──────────────────────────────── main ───────────────────────────────


def main():
    if not KEY:
        sys.exit("set API_KEY")

    status = api("GET", "/api/v1/firewall/status")
    if status.get("backend") != "nstun":
        sys.exit("egress backend is %r, not nstun — nothing to benchmark" % status.get("backend"))
    cp = status.get("control_plane_allow", {}).get("addrs") or []
    if not cp:
        sys.exit("no control-plane address in /firewall/status; cannot pick a reachable probe target")
    probe_addr = cp[0]

    # The hold-sink keeps one fd per saturating connection, so this process
    # needs more than the usual 1024 or it, not nstun, becomes the bottleneck.
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE, (min(hard, max(soft, 8192)), hard))

    held = []
    sink_sock, sink_port = listen_on(probe_addr, _sink)
    hold_sock, hold_port = listen_on(probe_addr, _hold, held)
    print("probe targets: allow-sink %s:%d (server closes), hold-sink %s:%d (never closes)"
          % (probe_addr, sink_port, probe_addr, hold_port))

    signal.signal(signal.SIGINT, _sig)
    signal.signal(signal.SIGTERM, _sig)
    atexit.register(cleanup)

    fn = api("POST", "/api/v1/functions", {
        "name": "nstun-bench-%d" % os.getpid(), "runtime": "node",
        "memory_mb": 256, "cpus": 1, "timeout_ms": 120000, "network_mode": "egress"})
    FN["id"] = fn["id"]
    api("POST", "/api/v1/functions/%s/deploy-inline" % FN["id"],
        {"code": HANDLER, "filename": "handler.js"})
    for _ in range(60):
        if api("GET", "/api/v1/functions/" + FN["id"])["status"] == "active":
            break
        time.sleep(1)
    else:
        sys.exit("probe function never went active")

    # Against the hold-sink, not the allow-sink: the allow-sink closes on accept
    # and Node then tears the guest socket down, so nothing accumulates and the
    # fd ceiling never shows itself.
    lim = invoke({"mode": "limits", "host": probe_addr, "port": hold_port, "probe_fds": 96})
    print("guest rlimit: %s  → %d concurrent sockets before %s"
          % (lim["nofile"], lim["max_concurrent_sockets"], lim["stopped_because"]))

    # ── Q1 ────────────────────────────────────────────────────────────
    allow = {s: [] for s in STEPS}
    rej_first = {s: [] for s in STEPS}
    rej_last = {s: [] for s in STEPS}
    v4 = {}
    print("\n== Q1: connect() latency vs blocklist size (%d rounds x %d samples) ==" % (ROUNDS, SAMPLES))
    for rnd in range(ROUNDS):
        # Rotate the visit order each round so a slow drift in host conditions
        # cannot line up with the ladder and look like a rule-count effect.
        order = STEPS[rnd % len(STEPS):] + STEPS[:rnd % len(STEPS)]
        for step in order:
            set_rules(step)
            st = api("GET", "/api/v1/firewall/status")
            v4[step] = st["policy_rule_counts"]["v4"]
            drain_pool()
            invoke({"mode": "latency", "host": probe_addr, "port": sink_port, "warm": 10, "n": 5})
            gen_ok = st["policy_generation"] in live_worker_generations()

            a = invoke({"mode": "latency", "host": probe_addr, "port": sink_port,
                        "warm": 20, "n": SAMPLES})
            allow[step].append(a["connected"])
            f = invoke({"mode": "latency", "host": FIRST_REJECT_IP, "port": 80,
                        "warm": 10, "n": 200, "connect_timeout_ms": 2000})
            rej_first[step].append((f["failed"], f["errors"]))
            deep = last_reject_ip(step)
            if deep:
                g = invoke({"mode": "latency", "host": deep, "port": 80,
                            "warm": 10, "n": 200, "connect_timeout_ms": 2000})
                rej_last[step].append((g["failed"], g["errors"]))
            print("  round %d  rules=%-5d v4=%-5d gen_in_worker=%-5s allow p50=%.3f  rej_first p50=%s  rej_last p50=%s"
                  % (rnd, step, v4[step], gen_ok, a["connected"]["p50"],
                     (f["failed"] or {}).get("p50"),
                     (rej_last[step][-1][0] or {}).get("p50") if deep else "-"))

    def med(rows, key):
        vals = [r[0][key] for r in rows if r[0]]
        return statistics.median(vals) if vals else float("nan")

    print("\n  all times in ms; allow path walks the whole vector, rejN is a full walk ending in a match")
    print("  %-7s %-6s | %-7s %-7s %-7s %-7s | %-8s %-8s | %-8s %-8s"
          % ("rules", "v4", "a_min", "a_p50", "a_p90", "a_p95", "rej1_min", "rej1_p50", "rejN_min", "rejN_p50"))
    for step in STEPS:
        A = allow[step]
        deep = ("%-8.3f %-8.3f" % (med(rej_last[step], "min"), med(rej_last[step], "p50"))
                if rej_last[step] else "%-8s %-8s" % ("-", "-"))
        print("  %-7d %-6d | %-7.3f %-7.3f %-7.3f %-7.3f | %-8.3f %-8.3f | %s"
              % (step, v4[step], min(x["min"] for x in A),
                 statistics.median([x["p50"] for x in A]),
                 statistics.median([x["p90"] for x in A]),
                 statistics.median([x["p95"] for x in A]),
                 med(rej_first[step], "min"), med(rej_first[step], "p50"), deep))
    codes = {}
    for step in STEPS:
        for rows in (rej_first[step], rej_last[step]):
            for _, errs in rows:
                for k, v in errs.items():
                    codes[k] = codes.get(k, 0) + v
    print("  reject outcomes: %s   (all should be ECONNREFUSED — a REJECT rule is a fast fail)" % codes)

    # ── Q2 ────────────────────────────────────────────────────────────
    if SKIP_FLOWCAP:
        print("\n== Q2 skipped (BENCH_SKIP_FLOWCAP=1) ==")
        return
    print("\n== Q2: nstun flow cap (NSTUN_MAX_FLOWS=1024, checked BEFORE policy) ==")
    set_rules(0)
    drain_pool()
    invoke({"mode": "latency", "host": probe_addr, "port": sink_port, "warm": 5, "n": 5})
    sat = invoke({"mode": "saturate", "host": probe_addr, "port": sink_port,
                  "hold_host": probe_addr, "hold_port": hold_port,
                  "blocked_host": FIRST_REJECT_IP, "blocked_port": 80,
                  "max_attempts": 2000, "budget_ms": 40000}, timeout=180)
    print("  before saturation : blocked=%s allowed=%s" % (sat["before"]["blocked"], sat["before"]["allowed"]))
    print("  saturated after   : %d guest-closed connections in %d ms (signal=%s)"
          % (sat["attempts_to_saturate"], sat["saturate_ms"], sat["saturation_signal"]))
    print("  at the cap        : blocked=%s allowed=%s" % (sat["at_cap"]["blocked"], sat["at_cap"]["allowed"]))
    rec = invoke({"mode": "recover", "blocked_host": FIRST_REJECT_IP, "blocked_port": 80,
                  "max_polls": 12}, timeout=180)
    print("  recovery          : %s after %ss idle"
          % ("recovered" if "recovered_after_s" in rec else "NOT recovered within the poll window",
             rec.get("recovered_after_s", rec["polls"][-1]["t_s"])))
    print("  held sockets on the host sink: %d" % len(held))
    for c in held:
        try:
            c.close()
        except OSError:
            pass
    sink_sock.close()
    hold_sock.close()


if __name__ == "__main__":
    try:
        main()
    finally:
        cleanup()
