#!/usr/bin/env python3
"""
test_providers.py — REST-only E2E for AI provider config CRUD + at-rest key
secrecy. No chat / mock LLM needed: this exercises /api/v1/ai/providers directly
and asserts the plaintext API key NEVER leaves the server in any response.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_providers.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import json
import sys

from harness import OrvaClient, section, check, summary

PROVIDER = "openai"
LABEL = "e2e-prov"
SECRET = "sk-secret-123"


def find_entry(c, label=LABEL, provider=PROVIDER):
    """Return the ProviderView dict for our test entry, or None."""
    lst = c.get("/api/v1/ai/providers") or {}
    for p in lst.get("providers", []):
        if p.get("provider") == provider and p.get("label") == label:
            return p
    return None


def cleanup(c):
    # Tolerant cleanup: delete every e2e-prov entry we (might have) created.
    try:
        lst = c.get("/api/v1/ai/providers") or {}
        for p in lst.get("providers", []):
            if p.get("provider") == PROVIDER and p.get("label") == LABEL:
                c.req("DELETE", f"/api/v1/ai/providers/{p['id']}", expect=(200, 204, 404))
    except Exception:
        pass


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    try:
        # ── 1. create a provider with a plaintext key ─────────────────────
        section("create provider (has_key True)")
        code, resp = c.req("POST", "/api/v1/ai/providers", {
            "provider": PROVIDER, "label": LABEL, "api_key": SECRET,
            "base_url": "http://x", "enabled": True,
        }, expect=(200, 201))
        check("create -> 200", code == 200, f"status {code}")
        prov = (resp or {}).get("provider") or {}
        check("response wraps a provider object", isinstance(prov, dict) and bool(prov), str(resp)[:200])
        check("created provider has an id", bool(prov.get("id")), str(prov)[:160])
        check("created provider has_key is True", prov.get("has_key") is True, str(prov))
        check("created provider enabled is True", prov.get("enabled") is True, str(prov))
        check("created provider base_url echoed", prov.get("base_url") == "http://x", str(prov))
        # CRITICAL: the create response must not leak the plaintext key either.
        check("create response does NOT contain plaintext key",
              SECRET not in json.dumps(resp), "plaintext key leaked in create response")
        pid = prov.get("id")

        # ── 2. list providers + at-rest secrecy ───────────────────────────
        section("list providers — key never leaves the server")
        code, lst = c.req("GET", "/api/v1/ai/providers", expect=(200,))
        check("list -> 200", code == 200, f"status {code}")
        entry = None
        for p in (lst or {}).get("providers", []):
            if p.get("id") == pid:
                entry = p
        check("entry is present in list", bool(entry), str(lst)[:200])
        check("listed entry has_key is True", bool(entry) and entry.get("has_key") is True, str(entry))
        # CRITICAL: raw JSON of the WHOLE list response must not contain the secret.
        check("list response does NOT contain plaintext key anywhere",
              SECRET not in json.dumps(lst), "plaintext key leaked in providers list")

        # ── 3. rotate the key (upsert keeps has_key True) ─────────────────
        section("rotate key (upsert by provider+label)")
        code, resp = c.req("POST", "/api/v1/ai/providers", {
            "provider": PROVIDER, "label": LABEL, "api_key": "sk-rotated-456",
            "base_url": "http://x", "enabled": True,
        }, expect=(200, 201))
        check("rotate -> 200", code == 200, f"status {code}")
        rprov = (resp or {}).get("provider") or {}
        check("rotate upserts the same row (same id)", rprov.get("id") == pid,
              f"{rprov.get('id')} != {pid}")
        check("rotate keeps has_key True", rprov.get("has_key") is True, str(rprov))
        check("rotate response does NOT contain rotated key",
              "sk-rotated-456" not in json.dumps(resp), "rotated key leaked")
        # Confirm there is still exactly one e2e-prov entry (upsert, not insert).
        entries = [p for p in (c.get("/api/v1/ai/providers") or {}).get("providers", [])
                   if p.get("provider") == PROVIDER and p.get("label") == LABEL]
        check("upsert did not create a duplicate row", len(entries) == 1, f"{len(entries)} entries")

        # ── 4. update without key preserves stored key, flips enabled ─────
        section("update with empty api_key preserves stored key")
        code, resp = c.req("POST", "/api/v1/ai/providers", {
            "provider": PROVIDER, "label": LABEL, "api_key": "",
            "base_url": "http://x", "enabled": False,
        }, expect=(200, 201))
        check("update -> 200", code == 200, f"status {code}")
        uprov = (resp or {}).get("provider") or {}
        check("empty key update keeps has_key True (stored key preserved)",
              uprov.get("has_key") is True, str(uprov))
        check("update flipped enabled to False", uprov.get("enabled") is False, str(uprov))
        check("update still the same row (same id)", uprov.get("id") == pid,
              f"{uprov.get('id')} != {pid}")

        # ── 5. delete -> 204, entry gone ──────────────────────────────────
        section("delete provider")
        code = c.delete(f"/api/v1/ai/providers/{pid}")
        check("delete -> 204", code == 204, f"status {code}")
        check("entry no longer listed after delete", find_entry(c) is None,
              "e2e-prov still present after delete")

    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
