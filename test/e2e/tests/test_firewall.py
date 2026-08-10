#!/usr/bin/env python3
"""Sandbox egress policy + DNS.

Three parts:
  1. API surface — rule CRUD, wildcard refusal, validation, the NSTUN policy
     status snapshot, and a GET+PUT round-trip of the DNS config (restored).
  2. Enforcement (needs a real sandbox) — a rule actually refuses a
     destination while an unblocked one still connects, the control-plane
     carve-out survives the shipped RFC1918 suggestions (so orva.kv keeps
     working), and a policy change recycles egress pools only.
  3. Fail-closed (needs a real sandbox AND access to the instance's data dir) —
     with the compiled policy taken away, an egress worker must refuse to run
     while a network_mode=none worker keeps serving.

Part 2 is the one that never existed: without it, egress rules could be a
silent no-op on every host and CI would still be green. Part 3 is the one that
matters most: NSTUN is default-ALLOW, so "no policy" must mean "no worker",
never "unfiltered worker".
"""
import json
import os
import subprocess
import sys
import time

from harness import OrvaClient, check, latest_execution_stderr, section, skip, summary

# Unique, domain-prefixed values so this module never collides with others.
HOST_VALUE = "e2e-fw-block.example.invalid"
CIDR_VALUE = "203.0.113.0/24"  # TEST-NET-3, safe documentation range
POLICY_CIDR = "203.0.113.128/25"  # distinct value → forces a new generation
WILDCARD_VALUE = "*.e2e-fw.example.invalid"
LABEL = "e2e-fw-custom"

EGRESS_FN = "e2e-fw-egress"
ISOLATED_FN = "e2e-fw-isolated"

# Anycast addresses with TCP/443 open, used as literal probe targets so the
# assertions never depend on DNS inside the sandbox.
#
# The blocked target is chosen at runtime from these candidates: the first one
# the sandbox can actually reach wins. Every candidate is an anycast resolver
# with TCP/443 (DoH) open, and none is one of Orva's default resolvers
# (1.1.1.1 / 8.8.8.8), so a REJECT rule for any of them cannot disturb sandbox
# DNS.
#
# This is a list rather than one host because the baseline leg is the single
# assertion here that genuinely requires the destination to be reachable — you
# cannot prove a rule blocked something that was never reachable in the first
# place. Pinning it to one public IP made the entire enforcement suite depend
# on that IP answering a CI runner: 9.9.9.9:443 timed out on the amd64 runner
# while arm64 passed the identical assertion on the same commit. Rotating
# candidates keeps the proof (reachable before -> refused after) without
# betting it on a single host.
BLOCK_CANDIDATES = ("9.9.9.9", "149.112.112.112", "208.67.222.222", "94.140.14.14")
BLOCK_RULES = tuple(f"{h}/32" for h in BLOCK_CANDIDATES)

# 8.8.8.8 is the control, and is deliberately NOT drawn from the candidates:
# the policy allows it on :53 only, so at :443 no rule matches it and NSTUN's
# default-allow must keep it reachable. That is the assertion — a DNS allow
# must not widen into a general allow for that host.
CONTROL_TARGET = "8.8.8.8"

# Shipped `suggested` rules (seeded disabled). Enabling them used to sever the
# internal SDK, because the control-plane address orvad hands sandboxes is
# itself RFC1918.
RFC1918_VALUES = ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")

REQUIRE_SANDBOX = os.environ.get("ORVA_REQUIRE_SANDBOX", "") in ("1", "true", "yes")

# One handler serves every sandbox assertion. mode=probe opens a raw TCP
# connection (no TLS, no HTTP) so "blocked" vs "reachable" is unambiguous;
# mode=kv exercises the internal SDK; mode=noop just warms the pool.
HANDLER_JS = """const net = require('net')
const { kv } = require('orva')

function probe(host, port, timeoutMs) {
  return new Promise((resolve) => {
    const sock = new net.Socket()
    let settled = false
    const finish = (result, code) => {
      if (settled) return
      settled = true
      try { sock.destroy() } catch (_) {}
      resolve({ result, code: code || null })
    }
    sock.setTimeout(timeoutMs)
    sock.once('connect', () => finish('connected', null))
    sock.once('timeout', () => finish('timeout', 'ETIMEDOUT'))
    sock.once('error', (e) => finish('error', (e && (e.code || e.message)) || 'unknown'))
    sock.connect(port, host)
  })
}

exports.handler = async (event) => {
  const body = typeof event.body === 'string'
    ? JSON.parse(event.body || '{}')
    : event.body || {}
  const mode = body.mode || 'noop'
  let out = { mode }
  if (mode === 'probe') {
    const host = body.host || '8.8.8.8'
    const port = Number(body.port || 443)
    out = { ...out, host, port, ...(await probe(host, port, Number(body.timeout_ms || 4000))) }
  } else if (mode === 'kv') {
    const key = 'e2e-fw-kv-probe'
    try {
      await kv.put(key, { probe: true })
      const got = await kv.get(key, null)
      await kv.delete(key)
      out = { ...out, kv_ok: !!(got && got.probe), kv_err: null }
    } catch (e) {
      out = { ...out, kv_ok: false, kv_err: String((e && (e.code || e.message)) || e) }
    }
  }
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(out),
  }
}
"""


def _rules(c):
    """GET /api/v1/firewall/rules -> the (possibly null) rules list, guarded."""
    resp = c.get("/api/v1/firewall/rules") or {}
    return (resp.get("rules") or []), resp


def _rule_by_value(c, value, kind=None):
    rules, _ = _rules(c)
    for r in rules:
        if r.get("value") == value and (kind is None or r.get("kind") == kind):
            return r
    return None


def _set_enabled(c, rule_id, enabled):
    return c.req("PUT", f"/api/v1/firewall/rules/{rule_id}", {"enabled": enabled},
                 expect=range(200, 599))[0]


def _json_body(body):
    if isinstance(body, str):
        try:
            return json.loads(body)
        except Exception:
            return {}
    return body if isinstance(body, dict) else {}


def sandbox_unavailable(reason):
    """Skip on API-only environments, but fail the mandatory engine gate."""
    if REQUIRE_SANDBOX:
        check("sandbox invocation is available", False, reason)
        return summary()
    return skip(reason)


def wait_active(c, fid, timeout=90):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        code, fn = c.req("GET", f"/api/v1/functions/{fid}", expect=range(200, 599))
        if code == 200 and isinstance(fn, dict):
            last = fn.get("status")
            if last in ("active", "error"):
                return last
        time.sleep(1.0)
    return last


def deploy(c, name, network_mode):
    """Create + deploy the shared handler. Returns (fid, terminal_status)."""
    body = {"name": name, "description": "egress policy e2e", "runtime": "node",
            "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
            "cpus": 1, "network_mode": network_mode, "auth_mode": "none"}
    code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
    if not (200 <= code < 300) or not isinstance(created, dict):
        return None, f"create -> {code}: {str(created)[:160]}"
    fid = created.get("id")
    if not fid:
        return None, f"create returned no id: {str(created)[:160]}"
    c.req("POST", f"/api/v1/functions/{fid}/deploy-inline",
          {"code": HANDLER_JS, "filename": "handler.js"}, expect=range(200, 599))
    return fid, wait_active(c, fid)


def force_cold_start(c, fid):
    """Retire the function's warm pool so the next invoke loads the current
    policy generation.

    A policy change retires egress pools by itself, but that recycle is
    rate-limited to one per 60s (a flapping DNS answer must not become a
    cold-start machine gun). A PUT that touches the spawn config drains the
    pool immediately, so the assertions below don't have to sit through the
    coalescing window.
    """
    c.req("PUT", f"/api/v1/functions/{fid}", {"memory_mb": 128}, expect=range(200, 599))


def invoke(c, fid, body):
    code, raw = c.req("POST", f"/fn/{fid}", body, expect=range(200, 599))
    return code, _json_body(raw)


def probe(c, fid, host, port=443):
    return invoke(c, fid, {"mode": "probe", "host": host, "port": port, "timeout_ms": 4000})


def pool_entry(c, fid):
    data = c.get("/api/v1/system/metrics.json") or {}
    for p in (data.get("pools") or []):
        if p.get("function_id") == fid:
            return p
    return None


# ── reaching the instance's data dir (fail-closed case only) ─────────────────
#
# Taking the compiled policy away from the daemon is something no API can do —
# by design — so this module has to touch the instance's filesystem. Two
# backends cover how the suite is actually run: a host process (ci.yml's e2e job
# runs `orva serve` on the VM under the same user as the suite) and the isolated
# Docker container spun by env.py. Anything else skips.
#
# NOTHING is touched until the candidate is PROVEN to be the instance under
# test: its first-boot admin key must equal the key this module authenticates
# with. Sabotaging the wrong Orva would be far worse than skipping.

def _run(cmd, timeout=20):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    except Exception:
        return None


class LocalFiles:
    """The instance runs on this host and its data dir is ours to write."""

    def __init__(self, data_dir):
        self.data_dir = data_dir
        self.where = f"host data dir {data_dir}"

    def _abs(self, rel):
        return os.path.join(self.data_dir, rel)

    def read(self, rel):
        try:
            with open(self._abs(rel)) as f:
                return f.read()
        except Exception:
            return ""

    def exists(self, rel):
        return os.path.exists(self._abs(rel))

    def move(self, src, dst):
        try:
            os.replace(self._abs(src), self._abs(dst))
            return True
        except Exception:
            return False

    def writable(self, rel):
        return os.access(self._abs(rel), os.W_OK)


class DockerFiles:
    """The instance is the isolated container env.py started."""

    def __init__(self, container, data_dir="/var/lib/orva"):
        self.container = container
        self.data_dir = data_dir
        self.where = f"container {container}:{data_dir}"

    def _abs(self, rel):
        return self.data_dir + "/" + rel

    def _exec(self, *args):
        return _run(["docker", "exec", self.container, *args])

    def read(self, rel):
        p = self._exec("cat", self._abs(rel))
        return p.stdout if p is not None and p.returncode == 0 else ""

    def exists(self, rel):
        p = self._exec("test", "-e", self._abs(rel))
        return p is not None and p.returncode == 0

    def move(self, src, dst):
        p = self._exec("mv", self._abs(src), self._abs(dst))
        return p is not None and p.returncode == 0

    def writable(self, rel):
        p = self._exec("test", "-w", self._abs(rel))
        return p is not None and p.returncode == 0


def _proc_data_dirs():
    """Data dirs of `orva serve` processes owned by this user (CI's layout:
    ORVA_DATA_DIR=$RUNNER_TEMP/orva-data, which nothing exports to the suite)."""
    found = []
    try:
        pids = [p for p in os.listdir("/proc") if p.isdigit()]
    except Exception:
        return found
    for pid in pids:
        try:
            with open(f"/proc/{pid}/cmdline", "rb") as f:
                argv = [a for a in f.read().split(b"\0") if a]
            if not argv or b"serve" not in argv or not argv[0].endswith(b"orva"):
                continue
            with open(f"/proc/{pid}/environ", "rb") as f:
                for entry in f.read().split(b"\0"):
                    if entry.startswith(b"ORVA_DATA_DIR="):
                        found.append(entry.split(b"=", 1)[1].decode(errors="replace"))
        except Exception:
            continue
    return found


def instance_files(c):
    """Return (files backend proven to belong to the instance under test, "")
    or (None, why-every-candidate-was-refused) for the skip message."""
    tried = []
    candidates = []
    for d in [os.environ.get("ORVA_E2E_DATA_DIR", ""), os.environ.get("ORVA_DATA_DIR", "")]:
        if d:
            candidates.append(LocalFiles(d))
    candidates += [LocalFiles(d) for d in _proc_data_dirs()]
    candidates.append(LocalFiles("/var/lib/orva"))
    for name in ("ORVA_E2E_CONTAINER", "ORVA_CONTAINER"):
        if os.environ.get(name):
            candidates.append(DockerFiles(os.environ[name]))
    candidates.append(DockerFiles("orva-e2e"))

    for fs in candidates:
        key = fs.read(".admin-key").strip()
        if not key:
            tried.append(f"{fs.where}: no readable .admin-key")
            continue
        if key != (c.key or "").strip():
            # A real Orva, but not the one under test. Never touch it.
            tried.append(f"{fs.where}: admin key belongs to a different instance")
            continue
        if not fs.writable("firewall/policy"):
            tried.append(f"{fs.where}: policy dir is not writable by this user")
            continue
        return fs, ""
    return None, "; ".join(tried[:4]) or "no candidate data dir found"


def cleanup(c):
    """Silent, best-effort teardown. Nothing here may print: the RESULT
    trailer must stay the last line of stdout for run.py to parse it."""
    try:
        lst = c.get("/api/v1/functions?limit=10000") or {}
        for f in (lst.get("functions") or []):
            if f.get("name") in (EGRESS_FN, ISOLATED_FN):
                c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=range(200, 599))
    except Exception:
        pass
    try:
        rules, _ = _rules(c)
        for r in rules:
            # Only ever touch what this module creates.
            if r.get("kind") == "custom" and r.get("value") in (
                    HOST_VALUE, CIDR_VALUE, POLICY_CIDR) + BLOCK_RULES:
                c.req("DELETE", f"/api/v1/firewall/rules/{r['id']}", expect=range(200, 599))
    except Exception:
        pass


def restore_policy_file(hidden):
    """Put a hidden policy generation back where the daemon published it.
    Silent and idempotent — also called from the module's finally."""
    if not hidden:
        return True
    fs, hidden_rel, policy_rel = hidden
    if not fs.exists(hidden_rel):
        return fs.exists(policy_rel)
    return fs.move(hidden_rel, policy_rel)


def restore_suggested(c, saved):
    """Put the shipped suggested rules back exactly as they were found."""
    for rule_id, enabled in (saved or {}).items():
        try:
            _set_enabled(c, rule_id, enabled)
        except Exception:
            pass


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    saved_dns = None
    saved_suggested = {}
    rid = None
    cidr_id = None
    # (files backend, hidden path, real path) while the compiled policy is
    # deliberately off disk — always put back, in finally, no matter what.
    hidden = None
    try:
        section("list rules (built-ins)")
        rules, resp = _rules(c)
        check("list rules -> dict with rules", isinstance(resp, dict) and "rules" in resp,
              str(resp)[:160])
        check("built-in/default rules present",
              any(r.get("kind") in ("default", "suggested") for r in rules),
              f"{len(rules)} rules, kinds={sorted({r.get('kind') for r in rules})}")

        section("policy status snapshot")
        status = resp.get("status") if isinstance(resp, dict) else None
        check("status snapshot present", isinstance(status, dict), str(status)[:160])
        status = status if isinstance(status, dict) else {}
        check("backend == nstun", status.get("backend") == "nstun", str(status.get("backend")))
        check("policy_generation is populated", bool(status.get("policy_generation")),
              str(status)[:200])
        check("policy is enforced", status.get("enforced") is True, str(status)[:200])
        # The nft table is gone, and so is the field that described it — no
        # compat alias, because reporting it as permanently true would be a lie.
        check("nftables_available key is gone", "nftables_available" not in status,
              str(sorted(status.keys()))[:240])
        counts = status.get("policy_rule_counts")
        check("policy_rule_counts breaks the policy down",
              isinstance(counts, dict) and all(k in counts for k in ("v4", "v6", "allow", "reject")),
              str(counts)[:160])
        check("compiled policy has allow rules (carve-outs)",
              isinstance(counts, dict) and (counts.get("allow") or 0) >= 1, str(counts)[:160])
        cp_allow = status.get("control_plane_allow")
        check("control_plane_allow is reported",
              isinstance(cp_allow, dict) and bool(cp_allow.get("addrs")), str(cp_allow)[:160])
        check("policy is not stale", status.get("policy_stale") is False,
              f"policy_stale={status.get('policy_stale')} err={str(status.get('last_compile_error'))[:120]}")

        section("standalone status endpoint")
        sc, sbody = c.req("GET", "/api/v1/firewall/status", expect=range(200, 599))
        check("get status -> 200", sc == 200, f"status {sc}: {str(sbody)[:160]}")
        check("standalone status reports the same backend",
              isinstance(sbody, dict) and sbody.get("backend") == "nstun", str(sbody)[:200])
        check("standalone status has no nftables_available",
              isinstance(sbody, dict) and "nftables_available" not in sbody, str(sbody)[:200])

        section("force resolve")
        rc, rbody = c.req("POST", "/api/v1/firewall/resolve", {}, expect=range(200, 599))
        check("resolve -> 200", rc == 200, f"status {rc}: {str(rbody)[:160]}")
        check("resolve reports refreshed", isinstance(rbody, dict) and rbody.get("refreshed") is True,
              str(rbody)[:200])
        check("resolve carries the status snapshot",
              isinstance(rbody, dict) and isinstance(rbody.get("status"), dict)
              and (rbody["status"].get("backend") == "nstun"), str(rbody)[:200])

        section("add custom hostname rule")
        code, created = c.req("POST", "/api/v1/firewall/rules",
                              {"rule_type": "hostname", "value": HOST_VALUE, "label": LABEL},
                              expect=range(200, 599))
        check("create -> 201", code == 201, f"status {code}: {str(created)[:160]}")
        rid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created has id", rid is not None)
        check("created kind=custom", isinstance(created, dict) and created.get("kind") == "custom")
        check("created value matches", isinstance(created, dict) and created.get("value") == HOST_VALUE)
        check("created rule_type=hostname",
              isinstance(created, dict) and created.get("rule_type") == "hostname")

        section("custom rule appears in list")
        rules, _ = _rules(c)
        check("custom rule listed by id", any(r.get("id") == rid for r in rules))
        check("custom rule listed by value",
              any(r.get("value") == HOST_VALUE and r.get("kind") == "custom" for r in rules))

        section("auto-detect CIDR rule type")
        # No rule_type supplied: a value containing '/' is auto-classified as cidr.
        cc, cidr = c.req("POST", "/api/v1/firewall/rules",
                         {"value": CIDR_VALUE, "label": LABEL}, expect=range(200, 599))
        check("cidr create -> 201", cc == 201, f"status {cc}: {str(cidr)[:160]}")
        cidr_id = (cidr or {}).get("id") if isinstance(cidr, dict) else None
        check("auto-detected rule_type=cidr",
              isinstance(cidr, dict) and cidr.get("rule_type") == "cidr",
              str(cidr)[:160])

        section("wildcards are refused, not half-enforced")
        # A packet policy matches addresses, not DNS names. A wildcard used to be
        # accepted and then silently resolved to its apex only — armed-looking,
        # blocking almost nothing. Both the malformed and the WELL-FORMED spelling
        # must now be rejected.
        wc, wbody = c.req("POST", "/api/v1/firewall/rules",
                          {"rule_type": "wildcard", "value": WILDCARD_VALUE},
                          expect=range(200, 599))
        check("well-formed wildcard rejected (4xx)", 400 <= wc < 500,
              f"status {wc}: {str(wbody)[:200]}")
        awc, awbody = c.req("POST", "/api/v1/firewall/rules",
                            {"value": WILDCARD_VALUE}, expect=range(200, 599))
        check("auto-detected wildcard rejected (4xx)", 400 <= awc < 500,
              f"status {awc}: {str(awbody)[:200]}")
        check("wildcard refusal explains itself",
              isinstance(wbody, dict) and "wildcard" in json.dumps(wbody).lower(),
              str(wbody)[:200])
        check("no wildcard row was created",
              _rule_by_value(c, WILDCARD_VALUE) is None)

        section("validation errors")
        ec, _ = c.req("POST", "/api/v1/firewall/rules", {"value": ""}, expect=range(200, 599))
        check("empty value rejected (4xx)", 400 <= ec < 500, f"status {ec}")
        bc, _ = c.req("POST", "/api/v1/firewall/rules",
                      {"rule_type": "wildcard", "value": "not-a-wildcard.com"},
                      expect=range(200, 599))
        check("malformed wildcard rejected (4xx)", 400 <= bc < 500, f"status {bc}")
        dup, _ = c.req("POST", "/api/v1/firewall/rules",
                       {"rule_type": "hostname", "value": HOST_VALUE}, expect=range(200, 599))
        check("duplicate value rejected (409)", dup == 409, f"status {dup}")

        section("delete custom rules")
        if rid is not None:
            dc, body = c.req("DELETE", f"/api/v1/firewall/rules/{rid}", expect=range(200, 599))
            check("delete -> 200", dc == 200, f"status {dc}: {str(body)[:160]}")
            check("delete body reports deleted",
                  isinstance(body, dict) and body.get("status") == "deleted")
            rules, _ = _rules(c)
            check("gone after delete", not any(r.get("id") == rid for r in rules))
            # Custom-only delete of an already-gone rule is rejected (not 404; 4xx).
            again = c.status("DELETE", f"/api/v1/firewall/rules/{rid}")
            check("re-delete rejected (4xx)", 400 <= again < 500, f"status {again}")
            rid = None
        if cidr_id is not None:
            dc2 = c.status("DELETE", f"/api/v1/firewall/rules/{cidr_id}")
            check("delete cidr rule -> 200", dc2 == 200, f"status {dc2}")
            cidr_id = None

        section("cannot delete a built-in (default) rule")
        rules, _ = _rules(c)
        builtin = next((r for r in rules if r.get("kind") in ("default", "suggested")), None)
        if builtin is not None:
            bdc = c.status("DELETE", f"/api/v1/firewall/rules/{builtin['id']}")
            check("built-in delete rejected (4xx)", 400 <= bdc < 500, f"status {bdc}")
        else:
            check("built-in delete rejected (4xx)", True, "no built-in rule to test against")

        section("DNS config get")
        gc, dns = c.req("GET", "/api/v1/firewall/dns", expect=range(200, 599))
        check("get dns -> 200", gc == 200, f"status {gc}")
        check("dns has servers list", isinstance(dns, dict) and isinstance(dns.get("servers"), list),
              str(dns)[:160])
        check("dns exposes defaults", isinstance(dns, dict) and isinstance(dns.get("defaults"), list))
        saved_dns = dns if isinstance(dns, dict) else None

        section("DNS config put round-trip")
        # Apply a harmless explicit resolver + search domain, verify it sticks,
        # then restore the original below in finally.
        pc, applied = c.req("PUT", "/api/v1/firewall/dns",
                            {"servers": ["1.1.1.1"], "search": "e2e-fw.internal", "records": []},
                            expect=range(200, 599))
        check("put dns -> 200", pc == 200, f"status {pc}: {str(applied)[:160]}")
        check("put echoes resolver",
              isinstance(applied, dict) and "1.1.1.1" in (applied.get("servers") or []),
              str(applied)[:160])
        check("put echoes search domain",
              isinstance(applied, dict) and applied.get("search") == "e2e-fw.internal")
        # Re-GET to confirm persistence (not just the echo).
        _, dns2 = c.req("GET", "/api/v1/firewall/dns", expect=range(200, 599))
        check("dns persisted on re-get",
              isinstance(dns2, dict) and "1.1.1.1" in (dns2.get("servers") or [])
              and dns2.get("search") == "e2e-fw.internal", str(dns2)[:160])

        section("DNS put validation")
        ivc = c.status("PUT", "/api/v1/firewall/dns", {"servers": ["not-an-ip"], "search": ""})
        check("non-IP resolver rejected (4xx)", 400 <= ivc < 500, f"status {ivc}")
        irc = c.status("PUT", "/api/v1/firewall/dns",
                       {"servers": [], "search": "", "records": [{"host": "db", "ip": "bogus"}]})
        check("bad record IP rejected (4xx)", 400 <= irc < 500, f"status {irc}")

        # ── enforcement: everything below needs a real sandbox ──────────────
        section("deploy egress + isolated probe functions")
        egress_fid, egress_status = deploy(c, EGRESS_FN, "egress")
        if egress_fid is None or egress_status != "active":
            return sandbox_unavailable(
                f"egress probe function never went active ({egress_status})")
        check("egress function active", egress_status == "active")
        iso_fid, iso_status = deploy(c, ISOLATED_FN, "none")
        if iso_fid is None or iso_status != "active":
            return sandbox_unavailable(f"isolated probe function never went active ({iso_status})")
        check("network_mode=none function active", iso_status == "active")

        wc_code, warm = invoke(c, egress_fid, {"mode": "noop"})
        if wc_code != 200:
            stderr = latest_execution_stderr(c, function_id=egress_fid)
            return sandbox_unavailable(
                f"egress invoke unavailable here ({wc_code}: {str(warm)[:200]}); "
                f"stderr={stderr[:500]!r}")
        check("egress invoke -> 200", wc_code == 200, str(warm)[:200])

        section("baseline reachability (no rule yet)")
        block_target, bc1, before = None, None, None
        for candidate in BLOCK_CANDIDATES:
            bc1, before = probe(c, egress_fid, candidate)
            before = before or {}
            if bc1 == 200 and before.get("result") == "connected":
                block_target = candidate
                break
            if before.get("code") == "EPERM":
                # Not a reachability problem: the seccomp policy denied
                # connect() outright, so no candidate can succeed. Stop here so
                # the diagnosis below is about seccomp, not about the network.
                break
        base_ok = check("a blockable destination is reachable before any rule",
                        block_target is not None,
                        f"none of {list(BLOCK_CANDIDATES)} reachable; "
                        f"last status {bc1}: {str(before)[:200]}")
        block_rule = f"{block_target}/32" if block_target else None
        if not base_ok:
            # Without outbound TCP from the sandbox there is nothing to block,
            # so the enforcement assertions cannot mean anything here.
            reason = f"sandbox has no outbound TCP; egress enforcement is untestable (probe said {str(before)[:200]})"
            if before.get("code") == "EPERM":
                # EPERM is the seccomp allowlist's DEFAULT ERRNO(1), not a
                # network problem: `connect` is absent from the default policy
                # (sandbox/seccomp.go — it only lands via networkSyscalls, which
                # `permissive` adds). With ORVA_SECCOMP_POLICY=default an egress
                # function cannot reach ANY destination, including orvad's own
                # internal SDK endpoints.
                reason = ("outbound connect() returned EPERM — the seccomp policy denied it. "
                          "The default policy omits `connect` (backend/internal/sandbox/seccomp.go: "
                          "networkSyscalls is only merged into `permissive`), so network_mode=egress "
                          "cannot connect anywhere and the egress policy has nothing to enforce. "
                          "Fix the policy or run with ORVA_SECCOMP_POLICY=permissive.")
            return sandbox_unavailable(reason)

        section("a rule actually blocks the destination")
        brc, brule = c.req("POST", "/api/v1/firewall/rules",
                           {"value": block_rule, "label": LABEL}, expect=range(200, 599))
        check("block rule created -> 201", brc == 201, f"status {brc}: {str(brule)[:200]}")
        block_id = (brule or {}).get("id") if isinstance(brule, dict) else None
        force_cold_start(c, egress_fid)

        ac, after = probe(c, egress_fid, block_target)
        check("blocked destination is refused", ac == 200 and after.get("result") != "connected",
              f"status {ac}: {str(after)[:200]}")
        # NSTUN REJECT synthesizes a refusal instead of blackholing the packet,
        # so a function fails fast with ECONNREFUSED rather than hanging.
        check("refusal surfaces as ECONNREFUSED", after.get("code") == "ECONNREFUSED",
              str(after)[:200])

        cc2, control = probe(c, egress_fid, CONTROL_TARGET)
        check("unblocked destination still connects",
              cc2 == 200 and control.get("result") == "connected",
              f"status {cc2}: {str(control)[:200]}")

        section("removing the rule restores reachability")
        if block_id is not None:
            dc3 = c.status("DELETE", f"/api/v1/firewall/rules/{block_id}")
            check("block rule deleted -> 200", dc3 == 200, f"status {dc3}")
            block_id = None
        force_cold_start(c, egress_fid)
        rc2, restored = probe(c, egress_fid, block_target)
        # Assert the POLICY stopped refusing, not that the runner can reach the
        # internet. A refusal by the compiled policy is ECONNREFUSED (NSTUN
        # answers with a RST); a timeout means the destination simply did not
        # answer, which is a property of the network the test happens to run
        # on. Asserting "connected" made this leg depend on a public host being
        # reachable from a CI runner — it failed on amd64 with ETIMEDOUT while
        # arm64 passed the identical assertion on the same commit.
        restored_code = (restored or {}).get("code")
        check(f"{block_target}:443 no longer refused after delete",
              rc2 == 200 and restored.get("result") != "refused"
              and restored_code != "ECONNREFUSED",
              f"status {rc2}: {str(restored)[:200]}")

        section("RFC1918 suggestions keep the internal SDK reachable")
        # Regression: the control-plane address orvad hands sandboxes is itself
        # RFC1918, so enabling the shipped private-network suggestions used to
        # break every orva.kv / jobs / F2F call. The compiler now emits the
        # carve-out (and nsjail's own gateway) ahead of the rejects.
        enabled_any = False
        for value in RFC1918_VALUES:
            rule = _rule_by_value(c, value, kind="suggested")
            if rule is None:
                continue
            saved_suggested[rule["id"]] = bool(rule.get("enabled"))
            code_en = _set_enabled(c, rule["id"], True)
            if code_en == 200:
                enabled_any = True
        check("shipped RFC1918 suggestions could be enabled", enabled_any,
              f"saved={saved_suggested}")
        force_cold_start(c, egress_fid)
        kc, kv_out = invoke(c, egress_fid, {"mode": "kv"})
        check("orva.kv works from an egress function with RFC1918 blocked",
              kc == 200 and kv_out.get("kv_ok") is True,
              f"status {kc}: {str(kv_out)[:240]}")
        restore_suggested(c, saved_suggested)
        saved_suggested = {}
        force_cold_start(c, egress_fid)

        section("policy change recycles egress pools only")
        # Warm both pools, then move the generation and watch which one is
        # retired. NSTUN loads its rules once per worker, so an egress worker
        # MUST be recycled; a network_mode=none worker has no policy to reload
        # and must be left alone.
        for _ in range(2):
            invoke(c, egress_fid, {"mode": "noop"})
            invoke(c, iso_fid, {"mode": "noop"})
        eg_before = pool_entry(c, egress_fid)
        iso_before = pool_entry(c, iso_fid)
        check("egress pool is warm", bool(eg_before) and (eg_before.get("spawned") or 0) >= 1,
              str(eg_before)[:200])
        check("isolated pool is warm", bool(iso_before) and (iso_before.get("spawned") or 0) >= 1,
              str(iso_before)[:200])
        iso_spawned = (iso_before or {}).get("spawned") or 0

        pc2, prule = c.req("POST", "/api/v1/firewall/rules",
                           {"value": POLICY_CIDR, "label": LABEL}, expect=range(200, 599))
        check("policy-moving rule created -> 201", pc2 == 201, f"status {pc2}: {str(prule)[:200]}")
        policy_rule_id = (prule or {}).get("id") if isinstance(prule, dict) else None

        # The recycle is rate-limited to one per 60s and drained on the next
        # 10s poll tick, so allow the full window plus slack. A recycle already
        # coalesced by an earlier section may land inside this wait too — the
        # discriminating assertion is what happens to the isolated pool.
        recycled, iso_after = False, iso_before
        deadline = time.time() + 100
        while time.time() < deadline:
            eg = pool_entry(c, egress_fid)
            iso_after = pool_entry(c, iso_fid)
            if eg is None or (eg.get("spawned") or 0) == 0:
                recycled = True
                break
            time.sleep(2)
        check("policy change retired the warm egress generation", recycled,
              f"egress pool still warm after 100s: {str(pool_entry(c, egress_fid))[:200]}")
        check("network_mode=none pool was not recycled",
              iso_after is not None and (iso_after.get("spawned") or 0) >= iso_spawned,
              f"before={str(iso_before)[:120]} after={str(iso_after)[:120]}")

        if policy_rule_id is not None:
            c.status("DELETE", f"/api/v1/firewall/rules/{policy_rule_id}")

        section("fail closed: no compiled policy, no egress worker")
        # The security posture in one assertion. NSTUN is default-ALLOW, so a
        # worker that starts without the policy it was supposed to load runs
        # completely unfiltered — the exact outcome the whole design exists to
        # prevent. With the compiled generation taken off disk, an egress
        # function must NOT serve, while a network_mode=none function (which
        # has no policy to load) must be untouched.
        #
        # This induces the "current generation is gone" variant. The other
        # variant — the daemon holding NO policy at all, which is what maps to
        # 503 EGRESS_POLICY_UNAVAILABLE — only happens when the very first
        # compile at boot fails, so reproducing it needs a cold start of the
        # instance. A test module is a client of an instance it did not launch
        # and must not restart it mid-suite, so that arm stays unit-covered
        # (backend/internal/server/handlers/firewall_test.go for the status
        # mapping, backend/internal/pool/egress_retire_test.go for the refusal
        # to spawn, backend/internal/sandbox/sandbox_test.go for the argv).
        # Both variants share the one property asserted here: no policy, no
        # egress worker.
        fs, why = instance_files(c)
        # Flush any pending rule edit into a published generation BEFORE
        # reading it: a generation change landing mid-section would republish
        # the file and silently undo the sabotage, leaving a test that passes
        # while proving nothing.
        c.req("POST", "/api/v1/firewall/resolve", {}, expect=range(200, 599))
        gen = (c.get("/api/v1/firewall/status") or {}).get("policy_generation") or ""
        if fs is None or not gen:
            reason = why if fs is None else "the instance reports no policy generation"
            print("  (fail-closed case skipped — this module must take the compiled "
                  "policy file off the instance's disk, which needs the data dir of "
                  f"the instance under test: {reason})")
        else:
            policy_rel = f"firewall/policy/egress-{gen}.cfg"
            check("the generation file the worker loads is on disk",
                  fs.exists(policy_rel), f"{fs.where} :: {policy_rel}")
            hidden_rel = policy_rel + ".e2e-hidden"
            if not fs.move(policy_rel, hidden_rel):
                check("compiled policy could be taken off disk", False,
                      f"{fs.where} :: mv {policy_rel} failed")
            else:
                hidden = (fs, hidden_rel, policy_rel)
                # Both pools must respawn: NSTUN reads its rules once per
                # worker, so a warm worker would still be running the policy it
                # started with and would prove nothing either way.
                force_cold_start(c, egress_fid)
                force_cold_start(c, iso_fid)

                fc, fout = probe(c, egress_fid, CONTROL_TARGET)
                stderr = latest_execution_stderr(c, function_id=egress_fid)
                detail = f"status {fc}: {str(fout)[:200]} stderr={stderr[:240]!r}"
                check("egress function does not serve without its policy",
                      fc != 200, detail)
                check("egress refusal is a server error, not a silent success",
                      500 <= fc < 600, detail)
                # If the worker had started anyway, this is what it would have
                # done: reach the internet with no rules loaded at all.
                check("no unfiltered outbound connection was made",
                      fout.get("result") != "connected", detail)

                ic, iout = invoke(c, iso_fid, {"mode": "noop"})
                check("network_mode=none keeps serving through the outage",
                      ic == 200, f"status {ic}: {str(iout)[:200]}")

                # ── recovery (the module may not leave the instance broken) ──
                restored = restore_policy_file(hidden)
                hidden = None
                check("compiled policy restored on disk",
                      restored and fs.exists(policy_rel), f"{fs.where} :: {policy_rel}")
                rrc, rrbody = c.req("POST", "/api/v1/firewall/resolve", {},
                                    expect=range(200, 599))
                check("forced resolve reports the policy enforced again",
                      rrc == 200 and isinstance(rrbody, dict)
                      and (rrbody.get("status") or {}).get("enforced") is True,
                      f"status {rrc}: {str(rrbody)[:200]}")
                force_cold_start(c, egress_fid)
                arc, aout = probe(c, egress_fid, CONTROL_TARGET)
                check("egress function serves again once the policy is back",
                      arc == 200 and aout.get("result") == "connected",
                      f"status {arc}: {str(aout)[:200]}")
    finally:
        # Restore the original DNS config + suggested-rule state so this module
        # leaves no trace. Silent by contract: the RESULT trailer must be last.
        # The policy file goes back FIRST: leaving it hidden would break egress
        # for every module that runs after this one.
        try:
            restore_policy_file(hidden)
        except Exception:
            pass
        restore_suggested(c, saved_suggested)
        if saved_dns is not None:
            try:
                c.req("PUT", "/api/v1/firewall/dns", {
                    "servers": saved_dns.get("servers") or [],
                    "search": saved_dns.get("search") or "",
                    "records": saved_dns.get("records") or [],
                }, expect=range(200, 599))
            except Exception:
                pass
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
