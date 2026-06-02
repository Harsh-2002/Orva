#!/usr/bin/env python3
"""Backup endpoint (admin): GET /api/v1/backup streams a non-empty gzip-tar DB
snapshot. We validate that backup PRODUCES output; we deliberately never touch
POST /api/v1/restore (destructive — it swaps the live DB and exits the process).

Routes (from router.go):
  GET  /api/v1/backup   -> BackupHandler.Download  (gzip tar of orva.db + functions/)
  POST /api/v1/restore  -> BackupHandler.Restore   (DESTRUCTIVE — not exercised)
"""
import sys
import urllib.error
import urllib.request

from harness import OrvaClient, section, check, summary


def _raw_get(c, path, timeout=120):
    """Fetch raw bytes (the backup body is binary gzip, not JSON, so we bypass
    the harness's JSON-decoding req() and read the wire bytes directly)."""
    r = urllib.request.Request(c.base + path, method="GET", headers=c._headers())
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read(), dict(resp.getheaders())
    except urllib.error.HTTPError as e:
        return e.code, e.read(), dict(e.headers)


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    # Nothing to create/clean up — backup is a read-only snapshot endpoint.
    try:
        section("backup download (GET /api/v1/backup)")
        code, raw, hdrs = _raw_get(c, "/api/v1/backup")
        check("GET backup -> 200", code == 200, f"status {code}: {raw[:160]!r}")
        check("backup body non-empty", bool(raw) and len(raw) > 0, f"len={len(raw or b'')}")
        # The handler writes a gzip stream; assert the gzip magic so we know we
        # got a real snapshot and not an error page that happened to be 200.
        check("body is gzip (magic 0x1f8b)",
              isinstance(raw, (bytes, bytearray)) and raw[:2] == b"\x1f\x8b",
              f"first bytes {bytes(raw[:4])!r}")
        # A real snapshot bundles orva.db + the functions/ tree; even an empty
        # instance's gzip-tar is well over a few hundred bytes.
        check("backup is a plausible snapshot size (>512B)", len(raw or b"") > 512,
              f"len={len(raw or b'')}")

        ctype = (hdrs.get("Content-Type") or hdrs.get("content-type") or "").lower()
        check("Content-Type is application/gzip", "gzip" in ctype, f"ctype={ctype!r}")
        cdisp = hdrs.get("Content-Disposition") or hdrs.get("content-disposition") or ""
        check("Content-Disposition attachment present", "attachment" in cdisp.lower(), f"cdisp={cdisp!r}")
        check("download filename looks like a backup", "orva-backup-" in cdisp and ".tar.gz" in cdisp,
              f"cdisp={cdisp!r}")

        section("backup is repeatable")
        code2, raw2, _ = _raw_get(c, "/api/v1/backup")
        check("second GET backup -> 200", code2 == 200, f"status {code2}")
        check("second backup also non-empty gzip",
              bool(raw2) and isinstance(raw2, (bytes, bytearray)) and raw2[:2] == b"\x1f\x8b",
              f"len={len(raw2 or b'')}")

        section("method validation")
        # Only GET is registered for /api/v1/backup (POST belongs to /restore),
        # so POST is refused — 405 (method not allowed) or 404 (no route);
        # either proves it isn't accepted.
        pc = c.status("POST", "/api/v1/backup", {})
        check("POST /api/v1/backup refused (404/405)", pc in (404, 405), f"status {pc}")

        section("auth required")
        # Backup is admin-gated by middleware_auth.go; an anonymous client must
        # not be able to pull a full DB snapshot.
        anon = OrvaClient(api_key="")
        ac = anon.status("GET", "/api/v1/backup")
        check("unauthenticated backup rejected (401/403)", ac in (401, 403), f"status {ac}")
    finally:
        pass
    return summary()


if __name__ == "__main__":
    sys.exit(main())
