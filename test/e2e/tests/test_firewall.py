#!/usr/bin/env python3
"""Sandbox egress policy + DNS.

Two halves:
  1. API surface — rule CRUD, wildcard refusal, validation, the NSTUN policy
     status snapshot, and a GET+PUT round-trip of the DNS config (restored).
  2. Enforcement (needs a real sandbox) — a rule actually refuses a
     destination while an unblocked one still connects, the control-plane
     carve-out survives the shipped RFC1918 suggestions (so orva.kv keeps
     working), and a policy change recycles egress pools only.

The enforcement half is the part that never existed: without it, egress rules
could be a silent no-op on every host and CI would still be green.
"""
import json
import os
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
# 9.9.9.9 (Quad9) is the one we block: it is NOT one of Orva's default
# resolvers (1.1.1.1 / 8.8.8.8), so a REJECT rule for it cannot disturb sandbox
# DNS. 8.8.8.8 is the control — the policy allows it on :53 only, so at :443 no
# rule matches it and NSTUN's default-allow must keep it reachable.
BLOCK_TARGET = "9.9.9.9"
BLOCK_RULE = "9.9.9.9/32"
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
                    HOST_VALUE, CIDR_VALUE, POLICY_CIDR, BLOCK_RULE):
                c.req("DELETE", f"/api/v1/firewall/rules/{r['id']}", expect=range(200, 599))
    except Exception:
        pass


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
        bc1, before = probe(c, egress_fid, BLOCK_TARGET)
        base_ok = check(f"{BLOCK_TARGET}:443 reachable before the rule",
                        bc1 == 200 and before.get("result") == "connected",
                        f"status {bc1}: {str(before)[:200]}")
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
                           {"value": BLOCK_RULE, "label": LABEL}, expect=range(200, 599))
        check("block rule created -> 201", brc == 201, f"status {brc}: {str(brule)[:200]}")
        block_id = (brule or {}).get("id") if isinstance(brule, dict) else None
        force_cold_start(c, egress_fid)

        ac, after = probe(c, egress_fid, BLOCK_TARGET)
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
        rc2, restored = probe(c, egress_fid, BLOCK_TARGET)
        check(f"{BLOCK_TARGET}:443 reachable again after delete",
              rc2 == 200 and restored.get("result") == "connected",
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
    finally:
        # Restore the original DNS config + suggested-rule state so this module
        # leaves no trace. Silent by contract: the RESULT trailer must be last.
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
