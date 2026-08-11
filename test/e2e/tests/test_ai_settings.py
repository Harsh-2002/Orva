#!/usr/bin/env python3
"""
test_settings.py — AI settings defaults, persistence, and enum validation (pure REST).
Exercises GET/PUT on /api/v1/ai/settings: confirms factory defaults, that editable
settings round-trip, that the internal tool budget cannot be overridden, and that
invalid enum values are rejected with HTTP 400.

Run:  ORVA_API_KEY=$(sudo cat /var/lib/orva/.admin-key) python3 test/ai/test_settings.py
(or set ORVA_URL/ORVA_API_KEY for any instance).
"""
import sys

from harness import (
    OrvaClient, section, check, summary,
)

SETTINGS = "/api/v1/ai/settings"


def get_settings(c):
    """GET /ai/settings unwraps the {"settings": {...}} envelope."""
    resp = c.get(SETTINGS) or {}
    return resp.get("settings") if isinstance(resp, dict) and "settings" in resp else resp

THINKING_LEVELS = {"off", "standard", "deep"}
APPROVAL_POLICIES = {"all_writes", "destructive_only", "auto"}

# Sane defaults restored in finally so other suites are unaffected.
DEFAULTS = {
    "provider": "anthropic",
    "model": "claude-opus-4-8",
    "thinking_level": "standard",
    "approval_policy": "all_writes",
    "max_tool_iterations": 25,
}


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    try:
        # ── 1. defaults / shape of the settings object ────────────────────
        section("settings defaults & shape")
        s = get_settings(c) or {}
        check("GET settings returns an object", isinstance(s, dict), str(s)[:200])
        check("provider default is anthropic", s.get("provider") == "anthropic", str(s.get("provider")))
        check("model default is claude-opus-4-8", s.get("model") == "claude-opus-4-8", str(s.get("model")))
        check("thinking_level is a valid enum", s.get("thinking_level") in THINKING_LEVELS, str(s.get("thinking_level")))
        check("approval_policy is a valid enum", s.get("approval_policy") in APPROVAL_POLICIES, str(s.get("approval_policy")))
        mti = s.get("max_tool_iterations")
        check("max_tool_iterations is the fixed internal budget", mti == 25, str(mti))
        sp = s.get("system_prompt")
        check("system_prompt is non-empty", isinstance(sp, str) and bool(sp.strip()), str(sp)[:80])

        # ── 2. valid PUT round-trips through GET ──────────────────────────
        section("valid PUT persists and reflects on GET")
        new = {
            "provider": "anthropic",
            "model": "claude-sonnet-4-6",
            "thinking_level": "deep",
            "approval_policy": "destructive_only",
            "max_tool_iterations": 15,
        }
        code, _ = c.req("PUT", SETTINGS, new, expect=range(200, 599))
        check("valid PUT returns 200", code == 200, f"PUT status {code}")
        s2 = get_settings(c) or {}
        check("GET reflects provider", s2.get("provider") == "anthropic", str(s2.get("provider")))
        check("GET reflects model", s2.get("model") == "claude-sonnet-4-6", str(s2.get("model")))
        check("GET reflects thinking_level", s2.get("thinking_level") == "deep", str(s2.get("thinking_level")))
        check("GET reflects approval_policy", s2.get("approval_policy") == "destructive_only", str(s2.get("approval_policy")))
        check("PUT cannot override the internal tool budget", s2.get("max_tool_iterations") == 25, str(s2.get("max_tool_iterations")))

        # ── 3. invalid enum values are rejected with 400 ──────────────────
        section("invalid enum values rejected (HTTP 400)")
        bad_thinking = dict(DEFAULTS)
        bad_thinking["thinking_level"] = "ultra"
        code, _ = c.req("PUT", SETTINGS, bad_thinking, expect=range(200, 599))
        check("invalid thinking_level -> 400", code == 400, f"PUT status {code}")

        bad_policy = dict(DEFAULTS)
        bad_policy["approval_policy"] = "yolo"
        code, _ = c.req("PUT", SETTINGS, bad_policy, expect=range(200, 599))
        check("invalid approval_policy -> 400", code == 400, f"PUT status {code}")

    finally:
        # Restore sane defaults so other suites are not affected.
        c.req("PUT", SETTINGS, DEFAULTS, expect=range(200, 599))
    return summary()


if __name__ == "__main__":
    sys.exit(main())
