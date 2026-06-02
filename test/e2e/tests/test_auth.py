#!/usr/bin/env python3
"""Auth + access control: /api/v1/auth/{status,me}, and the API-key gate on
protected /api/v1/* paths vs the public, auth-skipped /api/v1/system/health.

Read-only: this module never logs out, onboards, or mutates the admin user. It
only reads state and probes how unauthenticated/bad-key clients are rejected."""
import sys

from harness import OrvaClient, section, check, summary


def cleanup(c):
    # Nothing to create here — auth probing is read-only.
    pass


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    # Unauthenticated + bogus-key clients reuse the same base URL.
    anon = OrvaClient(base_url=c.base, api_key="")
    bogus = OrvaClient(base_url=c.base, api_key="orva_definitely_not_a_real_key_000")
    cleanup(c)
    try:
        section("auth status (public, establishes auth)")
        sc, st = c.req("GET", "/api/v1/auth/status", expect=range(200, 599))
        check("status -> 200", sc == 200, f"status {sc}")
        # A freshly-provisioned instance has an admin API key but no onboarded
        # WEB user, so has_user may be false; assert the endpoint reports a bool.
        check("status reports has_user (bool)",
              isinstance(st, dict) and isinstance(st.get("has_user"), bool), str(st)[:160])
        # /auth/* bypasses the API-key middleware entirely — reachable anonymously.
        asc, ast = anon.req("GET", "/api/v1/auth/status", expect=range(200, 599))
        check("status reachable WITHOUT auth -> 200", asc == 200, f"status {asc}")
        check("anon status returns has_user (bool)",
              isinstance(ast, dict) and isinstance(ast.get("has_user"), bool), str(ast)[:160])

        section("auth me (session-cookie identity, not API key)")
        # /auth/me reads the session cookie. An API-key client carries no
        # cookie, so it is UNAUTHENTICATED for this endpoint -> 401.
        mc, mbody = c.req("GET", "/api/v1/auth/me", expect=range(200, 599))
        check("me without a session cookie -> 401", mc == 401, f"status {mc}: {str(mbody)[:160]}")
        amc, _ = anon.req("GET", "/api/v1/auth/me", expect=range(200, 599))
        check("anon me -> 401", amc == 401, f"status {amc}")

        section("public endpoint reachable without auth")
        # system/health is explicitly skipped by the auth middleware (GET only).
        hc, hbody = anon.req("GET", "/api/v1/system/health", expect=range(200, 599))
        check("health reachable WITHOUT auth -> 200", hc == 200, f"status {hc}")
        check("health body is healthy",
              isinstance(hbody, dict) and hbody.get("status") == "healthy", str(hbody)[:160])

        section("protected endpoint requires a valid API key")
        # Admin key gets through.
        ok, _ = c.req("GET", "/api/v1/functions", expect=range(200, 599))
        check("admin key on /functions -> 200", ok == 200, f"status {ok}")
        # No key at all.
        an, _ = anon.req("GET", "/api/v1/functions", expect=range(200, 599))
        check("missing key on /functions -> 401", an == 401, f"status {an}")
        # Present-but-invalid key.
        bg, _ = bogus.req("GET", "/api/v1/functions", expect=range(200, 599))
        check("invalid key on /functions -> 401", bg == 401, f"status {bg}")

        section("other protected paths also gated")
        # A second real protected surface confirms the gate isn't /functions-specific.
        sk, _ = anon.req("GET", "/api/v1/executions", expect=range(200, 599))
        check("missing key on /executions -> 401", sk == 401, f"status {sk}")
        oke, _ = c.req("GET", "/api/v1/executions", expect=range(200, 599))
        check("admin key on /executions -> 200", oke == 200, f"status {oke}")
        # Admin reaches system/metrics.json; anon does not (it starts with /api/).
        am, _ = anon.req("GET", "/api/v1/system/metrics.json", expect=range(200, 599))
        check("missing key on /system/metrics.json -> 401", am == 401, f"status {am}")
        okm, _ = c.req("GET", "/api/v1/system/metrics.json", expect=range(200, 599))
        check("admin key on /system/metrics.json -> 200", okm == 200, f"status {okm}")
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
