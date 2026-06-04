#!/usr/bin/env python3
"""Inbound webhook triggers (v0.4): per-function signed external POST endpoints.

CRUD lives under /api/v1/functions/{fn_id}/inbound-webhooks (auth'd). The public
POST /webhook/{id} that external services hit is owned by a separate handler and
is NOT exercised here (no sandbox/invoke needed for this CRUD module).

Lifecycle: create function → create inbound trigger → get/list → update →
validation error → delete → confirm gone. Pure REST, no nsjail required.
"""
import sys

from harness import OrvaClient, section, check, summary

FN_NAME = "e2e-iwh-fn"
WH_NAME = "e2e-iwh-trigger"


def cleanup(c):
    lst = c.get("/api/v1/functions") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") == FN_NAME:
            fid = f.get("id")
            # Delete any inbound webhooks we left behind, then the function.
            ic, iwhs = c.req("GET", f"/api/v1/functions/{fid}/inbound-webhooks",
                             expect=range(200, 599))
            if ic == 200 and isinstance(iwhs, dict):
                for w in (iwhs.get("inbound_webhooks") or []):
                    c.req("DELETE", f"/api/v1/functions/{fid}/inbound-webhooks/{w['id']}",
                          expect=(200, 204, 404))
            c.req("DELETE", f"/api/v1/functions/{fid}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    fid = None
    wid = None
    try:
        section("setup function")
        fbody = {"name": FN_NAME, "description": "inbound webhook host",
                 "runtime": "node", "entrypoint": "handler.js",
                 "timeout_ms": 30000, "memory_mb": 128, "cpus": 1,
                 "network_mode": "none", "auth_mode": "none"}
        fc, fn = c.req("POST", "/api/v1/functions", fbody, expect=range(200, 599))
        check("create function -> 2xx", 200 <= fc < 300, f"status {fc}: {str(fn)[:160]}")
        fid = (fn or {}).get("id") if isinstance(fn, dict) else None
        check("function has id", bool(fid))
        if not fid:
            return summary()

        section("create inbound trigger")
        cbody = {"name": WH_NAME, "signature_format": "hmac_sha256_hex"}
        cc, created = c.req("POST", f"/api/v1/functions/{fid}/inbound-webhooks",
                            cbody, expect=range(200, 599))
        check("create -> 201", cc == 201, f"status {cc}: {str(created)[:160]}")
        wh = (created or {}).get("inbound_webhook") if isinstance(created, dict) else None
        wid = (wh or {}).get("id")
        check("response has inbound_webhook with id", bool(wid))
        check("name matches", isinstance(wh, dict) and wh.get("name") == WH_NAME)
        check("signature_format echoed", isinstance(wh, dict)
              and wh.get("signature_format") == "hmac_sha256_hex")
        # Domain-specific: plaintext secret is returned exactly once on create,
        # and the default header is stamped for the chosen format.
        check("plaintext secret returned once", bool((created or {}).get("secret")))
        check("trigger_url points at /webhook/<id>",
              isinstance(created, dict)
              and created.get("trigger_url") == f"/webhook/{wid}")
        check("default header for hmac_sha256_hex",
              isinstance(wh, dict) and wh.get("signature_header") == "X-Orva-Signature")
        check("active defaults true", isinstance(wh, dict) and wh.get("active") is True)

        section("get + list")
        if wid:
            gc, got = c.req("GET", f"/api/v1/functions/{fid}/inbound-webhooks/{wid}",
                            expect=range(200, 599))
            check("get by id -> 200", gc == 200, f"status {gc}")
            check("get returns same id", isinstance(got, dict) and got.get("id") == wid)
            # Domain-specific: plaintext secret must NEVER come back after create;
            # only the preview is exposed.
            check("get hides plaintext secret",
                  isinstance(got, dict) and not got.get("secret"))
            check("get exposes secret_preview",
                  isinstance(got, dict) and bool(got.get("secret_preview")))
        lc, lst = c.req("GET", f"/api/v1/functions/{fid}/inbound-webhooks",
                        expect=range(200, 599))
        check("list -> 200", lc == 200, f"status {lc}")
        rows = (lst or {}).get("inbound_webhooks") if isinstance(lst, dict) else None
        check("trigger appears in list",
              any(w.get("id") == wid for w in (rows or [])))

        section("update")
        if wid:
            uc, upd = c.req("PUT", f"/api/v1/functions/{fid}/inbound-webhooks/{wid}",
                            {"name": WH_NAME + "-renamed", "active": False},
                            expect=range(200, 599))
            check("update -> 200", uc == 200, f"status {uc}")
            check("name updated", isinstance(upd, dict)
                  and upd.get("name") == WH_NAME + "-renamed")
            check("active toggled off", isinstance(upd, dict) and upd.get("active") is False)

        section("validation")
        # Unknown signature_format is rejected (closed enum).
        bc, _ = c.req("POST", f"/api/v1/functions/{fid}/inbound-webhooks",
                      {"name": "e2e-iwh-bad", "signature_format": "not-a-format"},
                      expect=range(200, 599))
        check("bad signature_format rejected (4xx)", 400 <= bc < 500, f"status {bc}")
        # Missing name is rejected.
        nc, _ = c.req("POST", f"/api/v1/functions/{fid}/inbound-webhooks",
                      {"signature_format": "hmac_sha256_hex"}, expect=range(200, 599))
        check("missing name rejected (4xx)", 400 <= nc < 500, f"status {nc}")

        section("delete")
        if wid:
            dc, _ = c.req("DELETE", f"/api/v1/functions/{fid}/inbound-webhooks/{wid}",
                          expect=range(200, 599))
            check("delete -> 2xx", dc in (200, 204), f"status {dc}")
            check("gone after delete (404)",
                  c.status("GET", f"/api/v1/functions/{fid}/inbound-webhooks/{wid}") == 404)
            wid = None
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
