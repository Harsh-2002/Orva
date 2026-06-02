#!/usr/bin/env python3
"""System endpoints: health, metrics, runtimes, storage."""
import sys

from harness import OrvaClient, section, check, summary


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    section("health")
    h = c.get("/api/v1/system/health")
    check("status healthy", isinstance(h, dict) and h.get("status") == "healthy", str(h)[:160])
    check("has version", bool(h.get("version")))
    check("database ok", (h.get("database") or {}).get("status") == "ok")

    section("metrics")
    check("metrics (prometheus) -> 200", c.status("GET", "/api/v1/system/metrics") == 200)
    check("metrics.json -> 200", c.status("GET", "/api/v1/system/metrics.json") == 200)

    section("runtimes")
    rt = c.get("/api/v1/runtimes")
    runtimes = rt.get("runtimes") if isinstance(rt, dict) else rt
    check("runtimes listed", bool(runtimes), str(rt)[:160])

    section("storage (admin)")
    check("storage -> 200", c.status("GET", "/api/v1/system/storage") == 200)

    return summary()


if __name__ == "__main__":
    sys.exit(main())
