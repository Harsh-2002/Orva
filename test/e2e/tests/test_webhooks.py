#!/usr/bin/env python3
"""System-event webhook subscriptions: create, list (secret never plaintext),
get, update, test-fire, validation, delete.

Routes (backend/internal/server/router.go):
  GET    /api/v1/webhooks
  POST   /api/v1/webhooks
  GET    /api/v1/webhooks/{id}
  PUT    /api/v1/webhooks/{id}
  DELETE /api/v1/webhooks/{id}
  POST   /api/v1/webhooks/{id}/test
  GET    /api/v1/webhooks/{id}/deliveries

Create requires {name, url, events[]}. The plaintext signing secret is returned
exactly once in the create response's top-level `secret` field; the subscription
object carries only `secret_preview` (first 8 chars + ellipsis) and the DB struct
tags Secret as json:"-", so it must never appear in list/get/update bodies.
"""
import sys

from harness import OrvaClient, section, check, summary

NAME = "e2e-wh-sub"
URL = "https://example.invalid/e2e-webhook-receiver"


def cleanup(c):
    lst = c.get("/api/v1/webhooks") or {}
    for s in (lst.get("subscriptions") or []):
        if s.get("name") == NAME:
            c.req("DELETE", f"/api/v1/webhooks/{s['id']}", expect=(200, 204, 404))


def has_plaintext_secret(obj):
    """True if a subscription dict carries a full (non-preview) signing secret.
    The preview ends in the ellipsis char; the plaintext secret is 64 hex chars.
    The struct tags Secret json:"-" so this should never be present."""
    if not isinstance(obj, dict):
        return False
    for key in ("secret", "Secret"):
        v = obj.get(key)
        if isinstance(v, str) and v and not v.endswith("…"):
            return True
    return False


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    wid = None
    try:
        section("create subscription")
        body = {"name": NAME, "url": URL,
                "events": ["deployment.failed", "job.failed"], "enabled": True}
        code, created = c.req("POST", "/api/v1/webhooks", body, expect=range(200, 599))
        check("create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:200]}")
        sub = (created or {}).get("subscription") if isinstance(created, dict) else None
        wid = sub.get("id") if isinstance(sub, dict) else None
        check("created has id", bool(wid))
        check("created name matches", isinstance(sub, dict) and sub.get("name") == NAME)
        check("created url matches", isinstance(sub, dict) and sub.get("url") == URL)
        check("created events preserved",
              isinstance(sub, dict) and set(sub.get("events") or []) == {"deployment.failed", "job.failed"},
              str(sub.get("events") if isinstance(sub, dict) else None))

        section("signing secret is returned once at create, never after")
        plaintext = (created or {}).get("secret") if isinstance(created, dict) else None
        check("create echoes plaintext secret once", isinstance(plaintext, str) and len(plaintext) >= 16,
              f"got {plaintext!r}")
        check("subscription object exposes only preview, not plaintext",
              not has_plaintext_secret(sub))
        check("subscription object has secret_preview",
              isinstance(sub, dict) and isinstance(sub.get("secret_preview"), str)
              and sub.get("secret_preview").endswith("…"),
              str(sub.get("secret_preview") if isinstance(sub, dict) else None))

        section("get + list")
        if wid:
            gc, got = c.req("GET", f"/api/v1/webhooks/{wid}", expect=range(200, 599))
            check("get by id -> 200", gc == 200, f"status {gc}")
            check("get returns same id", isinstance(got, dict) and got.get("id") == wid)
            check("get does NOT leak plaintext secret", not has_plaintext_secret(got))
        lst = c.get("/api/v1/webhooks") or {}
        subs = lst.get("subscriptions") or []
        mine = next((s for s in subs if s.get("name") == NAME), None)
        check("appears in list", mine is not None)
        check("list does NOT leak plaintext secret",
              all(not has_plaintext_secret(s) for s in subs))

        section("update (PUT subset)")
        if wid:
            uc, upd = c.req("PUT", f"/api/v1/webhooks/{wid}",
                            {"events": ["*"], "enabled": False}, expect=range(200, 599))
            check("update -> 200", uc == 200, f"status {uc}: {str(upd)[:160]}")
            check("update applied events", isinstance(upd, dict) and upd.get("events") == ["*"],
                  str(upd.get("events") if isinstance(upd, dict) else None))
            check("update applied enabled=false", isinstance(upd, dict) and upd.get("enabled") is False)
            check("update does NOT leak plaintext secret", not has_plaintext_secret(upd))

        section("test fire")
        if wid:
            tc, tres = c.req("POST", f"/api/v1/webhooks/{wid}/test", {}, expect=range(200, 599))
            check("test -> 2xx (queued)", 200 <= tc < 300, f"status {tc}: {str(tres)[:160]}")
            check("test returns delivery_id",
                  isinstance(tres, dict) and bool(tres.get("delivery_id")), str(tres)[:160])
            dl = c.get(f"/api/v1/webhooks/{wid}/deliveries") or {}
            check("deliveries listable", isinstance(dl.get("deliveries"), list), str(dl)[:160])

        section("validation")
        # missing name -> 400
        nc, _ = c.req("POST", "/api/v1/webhooks",
                      {"url": URL, "events": ["job.failed"]}, expect=range(200, 599))
        check("missing name rejected (4xx)", 400 <= nc < 500, f"status {nc}")
        # non-http(s) url -> 400
        bc, _ = c.req("POST", "/api/v1/webhooks",
                      {"name": "e2e-wh-badurl", "url": "ftp://nope", "events": ["*"]},
                      expect=range(200, 599))
        check("non-http url rejected (4xx)", 400 <= bc < 500, f"status {bc}")
        # unknown event name -> 400
        ec, _ = c.req("POST", "/api/v1/webhooks",
                      {"name": "e2e-wh-badevent", "url": URL, "events": ["not.a.real.event"]},
                      expect=range(200, 599))
        check("unknown event name rejected (4xx)", 400 <= ec < 500, f"status {ec}")

        section("delete")
        if wid:
            dc, _ = c.req("DELETE", f"/api/v1/webhooks/{wid}", expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            check("gone after delete (404)", c.status("GET", f"/api/v1/webhooks/{wid}") == 404)
            wid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
