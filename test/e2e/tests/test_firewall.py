#!/usr/bin/env python3
"""Egress firewall + DNS: list rules (built-ins present), add/list/delete a custom
rule, validation errors, and a GET+PUT round-trip of the DNS config (restored)."""
import sys

from harness import OrvaClient, section, check, summary

# Unique, domain-prefixed values so this module never collides with others.
HOST_VALUE = "e2e-fw-block.example.invalid"
CIDR_VALUE = "203.0.113.0/24"  # TEST-NET-3, safe documentation range
LABEL = "e2e-fw-custom"


def _rules(c):
    """GET /api/v1/firewall/rules -> the (possibly null) rules list, guarded."""
    resp = c.get("/api/v1/firewall/rules") or {}
    return (resp.get("rules") or []), resp


def cleanup(c):
    rules, _ = _rules(c)
    for r in rules:
        # Only ever touch what this module creates (custom rules with our values).
        if r.get("kind") == "custom" and r.get("value") in (HOST_VALUE, CIDR_VALUE):
            c.req("DELETE", f"/api/v1/firewall/rules/{r['id']}", expect=range(200, 599))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    saved_dns = None
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
        check("status snapshot present (nftables availability)",
              isinstance(resp.get("status"), dict) and "nftables_available" in (resp.get("status") or {}),
              str(resp.get("status"))[:160])

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

        section("validation errors")
        ec, _ = c.req("POST", "/api/v1/firewall/rules", {"value": ""}, expect=range(200, 599))
        check("empty value rejected (4xx)", 400 <= ec < 500, f"status {ec}")
        bc, _ = c.req("POST", "/api/v1/firewall/rules",
                      {"rule_type": "wildcard", "value": "not-a-wildcard.com"},
                      expect=range(200, 599))
        check("wildcard without '*.' rejected (4xx)", 400 <= bc < 500, f"status {bc}")
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
    finally:
        # Restore the original DNS config so this module leaves no trace.
        if saved_dns is not None:
            c.req("PUT", "/api/v1/firewall/dns", {
                "servers": saved_dns.get("servers") or [],
                "search": saved_dns.get("search") or "",
                "records": saved_dns.get("records") or [],
            }, expect=range(200, 599))
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
