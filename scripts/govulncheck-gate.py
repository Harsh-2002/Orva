#!/usr/bin/env python3
"""Fail on any call-reachable govulncheck finding except a pinned, expiring allowlist.

govulncheck exits 0 under -format json, so this script is the gate:
  govulncheck -format json ./... > out.json && govulncheck-gate.py out.json
"""
import datetime
import json
import sys

ALLOW = {
    # mark3labs/mcp-go redirect SSRF, reached only via Bifrost's MCP client, which
    # Orva never builds (llm.TestGatewayHasNoMCPManager). No upstream fix as of
    # 2026-11-07: mark3labs/mcp-go#1188 / #1190. Delete this entry once a fixed
    # mcp-go is required by a Bifrost release that builds with our tree.
    "GO-2026-6911": {
        "module": "github.com/mark3labs/mcp-go",
        "expires": datetime.date(2026, 12, 7),
        "frames": (
            "github.com/mark3labs/mcp-go/",
            "github.com/maximhq/bifrost/core",
            "github.com/Harsh-2002/Orva/backend/internal/ai/llm",
        ),
    },
}


def messages(path):
    dec, text, i = json.JSONDecoder(), open(path).read(), 0
    while True:
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            return
        obj, i = dec.raw_decode(text, i)
        yield obj


def main():
    msgs = list(messages(sys.argv[1]))
    if not any("config" in m for m in msgs):
        print("FAIL: no govulncheck config message; the scan did not run")
        return 1

    called = {}
    for m in msgs:
        f = m.get("finding")
        if f and f["trace"][0].get("function"):
            called.setdefault(f["osv"], []).append(f)

    bad = []
    for osv, findings in sorted(called.items()):
        a = ALLOW.get(osv)
        if not a:
            bad.append(f"{osv}: call-reachable and not allowlisted")
            continue
        if datetime.date.today() > a["expires"]:
            bad.append(f"{osv}: exception expired {a['expires']}; re-review or fix")
        if any(f.get("fixed_version") for f in findings):
            bad.append(f"{osv}: a fix is now available; upgrade and delete the exception")
        if any(f["trace"][0]["module"] != a["module"] for f in findings):
            bad.append(f"{osv}: now reported in a different module")
        outside = {
            t.get("package", "")
            for f in findings
            for t in f["trace"]
            if not t.get("package", "").startswith(a["frames"])
        }
        if outside:
            bad.append(f"{osv}: trace leaves the allowed packages: {sorted(outside)}")
        print(f"allowed until {a['expires']}: {osv} ({len(findings)} traces)")

    for osv in ALLOW:
        if osv not in called:
            print(f"NOTE: {osv} is no longer reachable; delete its ALLOW entry")
    for b in bad:
        print("FAIL:", b)
    return 1 if bad else 0


sys.exit(main())
