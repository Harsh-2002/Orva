#!/usr/bin/env python3
"""CLI parity: the same binary's subcommands talk to the running server.

The Orva binary is server + CLI. CLIRunner runs `orva <args> --endpoint --api-key`
against the instance under test. We confirm the read-only surfaces (system health,
functions list) work and that the CLI observes server-side state (a function created
via REST shows up in `orva functions list`). Optionally exercises `orva deploy` +
`orva invoke`; those need a real build sandbox (nsjail), so they skip gracefully
when the build/invoke can't complete — the read-only checks still run and must pass.
"""
import json
import os
import sys
import tempfile

from harness import OrvaClient, CLIRunner, section, check, summary, skip

NAME = "e2e-cli-fn"
DEPLOY_NAME = "e2e-cli-deploy"


def cleanup(c):
    lst = c.get("/api/v1/functions?limit=10000") or {}
    for f in (lst.get("functions") or []):
        if f.get("name") in (NAME, DEPLOY_NAME):
            c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=(200, 204, 404))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    cli = CLIRunner()
    if not cli.available():
        return skip("orva binary not built (set ORVA_BIN / run `make build`)")

    cleanup(c)
    fid = None
    try:
        section("orva system health")
        rc, out, err = cli.run("system", "health")
        check("system health -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        blob = (out + err).lower()
        check("output mentions healthy", "healthy" in blob, out.strip()[:160])
        check("output mentions version", '"version"' in (out + err) or "version" in blob,
              out.strip()[:160])

        section("orva system metrics")
        rc, out, err = cli.run("system", "metrics")
        check("system metrics -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")

        section("orva functions list (empty / baseline)")
        rc, out, err = cli.run("functions", "list")
        check("functions list -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        check("list output has a header", "NAME" in out and "RUNTIME" in out,
              out.strip()[:160])

        section("CLI sees server state (created via REST)")
        # Create a function through the REST client, then assert the CLI lists it.
        body = {"name": NAME, "description": "cli parity", "runtime": "node",
                "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        code, created = c.req("POST", "/api/v1/functions", body, expect=range(200, 599))
        check("REST create -> 2xx", 200 <= code < 300, f"status {code}: {str(created)[:160]}")
        fid = (created or {}).get("id") if isinstance(created, dict) else None
        check("created has id", bool(fid))

        rc, out, err = cli.run("functions", "list")
        check("functions list (after create) -> exit 0", rc == 0,
              f"rc={rc} err={err.strip()[:160]}")
        check("CLI list shows the REST-created function", NAME in out, out.strip()[:200])
        if fid:
            check("CLI list shows its id", fid in out, out.strip()[:200])

        section("orva functions get (by name + by id)")
        rc, out, err = cli.run("functions", "get", NAME)
        check("functions get by name -> exit 0", rc == 0,
              f"rc={rc} err={err.strip()[:160]}")
        check("get output contains the name", NAME in out, out.strip()[:200])
        if fid:
            rc, out, err = cli.run("functions", "get", fid)
            check("functions get by id -> exit 0", rc == 0,
                  f"rc={rc} err={err.strip()[:160]}")
            check("get-by-id output contains the id", fid in out, out.strip()[:200])

        section("orva secrets list (CLI -> per-function REST)")
        rc, out, err = cli.run("secrets", "list", NAME)
        check("secrets list -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")

        section("orva kv list (CLI -> per-function REST)")
        rc, out, err = cli.run("kv", "list", NAME)
        check("kv list -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")

        section("CLI error handling (bad subcommand / args)")
        rc, out, err = cli.run("functions", "get")  # missing required arg
        check("functions get with no arg -> nonzero", rc != 0, f"rc={rc}")
        rc, out, err = cli.run("nope-not-a-command")
        check("unknown command -> nonzero", rc != 0, f"rc={rc}")
        rc, out, err = cli.run("invoke", "nope-not-real", "--body", "{}")
        check("invoke of missing function -> nonzero", rc != 0, f"rc={rc}")
        check("missing-function error names the function", "nope-not-real" in err,
              err.strip()[:160])

        section("global output contract (-o json, --quiet, stdout/stderr split)")
        rc, out, err = cli.run("functions", "list", "-o", "json")
        check("functions list -o json -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        parsed = None
        try:
            parsed = json.loads(out)
        except Exception as e:
            check("-o json emits parseable JSON on stdout", False, f"{e}: {out.strip()[:160]}")
        if parsed is not None:
            check("-o json emits parseable JSON on stdout", True)
            names = [f.get("name") for f in (parsed.get("functions") or [])]
            check("-o json payload contains the function", NAME in names, str(names)[:160])
        # --quiet keeps status (the "Total:" line) off stderr; data stays on stdout.
        rc, out, err = cli.run("functions", "list", "--quiet")
        check("functions list --quiet -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        check("--quiet suppresses stderr status", "Total" not in err, err.strip()[:160])
        check("--quiet still emits data on stdout", NAME in out, out.strip()[:160])

        section("new read-only command groups reach the server")
        for label, args in [
            ("system storage", ("system", "storage")),
            ("traces list", ("traces", "list", "--limit", "5")),
            ("firewall list", ("firewall", "list")),
            ("dns get", ("dns", "get")),
            ("deployments list", ("deployments", "list", NAME)),
        ]:
            rc, out, err = cli.run(*args)
            check(f"{label} -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")

        section("orva docs (embedded reference) + completion")
        rc, out, err = cli.run("docs", "--raw")
        check("docs --raw -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        check("docs --raw emits the reference markdown", "# Orva" in out[:400],
              out.strip()[:160])
        # origin placeholder must be substituted (no raw {{ORIGIN}} left).
        check("docs origin placeholder substituted", "{{ORIGIN}}" not in out,
              "found unsubstituted {{ORIGIN}} in docs output")
        rc, out, err = cli.run("completion", "bash")
        check("completion bash -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:160]}")
        check("completion script references orva", "orva" in out, out.strip()[:120])

        section("orva deploy + invoke (nsjail-dependent)")
        # Build a trivial node source dir and try a real deploy via the CLI.
        # API-only runs skip when nsjail is unavailable; the mandatory engine
        # gate converts the same condition into a failure.
        deploy_ok = _try_deploy_invoke(cli)
        if deploy_ok is None or deploy_ok.get("failed"):
            if os.environ.get("ORVA_REQUIRE_SANDBOX", "") in ("1", "true", "yes"):
                check("CLI deploy/invoke sandbox path is available", False,
                      (deploy_ok or {}).get("detail", "deploy or invoke did not complete successfully")[:300])
            else:
                print("  (deploy/invoke skipped — build sandbox unavailable)")
        else:
            # Reaching here means the CLI deploy reached a success status.
            check("CLI deploy succeeded", True, deploy_ok.get("detail", "")[:200])
            check("CLI invoke returned a response", deploy_ok.get("invoked", False),
                  deploy_ok.get("invoke_detail", "")[:200])

        section("delete via CLI + confirm gone")
        if fid:
            # --yes is required: destructive ops prompt interactively and refuse
            # on a non-TTY (like this harness) without it.
            rc, out, err = cli.run("functions", "delete", NAME, "--yes")
            check("functions delete -> exit 0", rc == 0,
                  f"rc={rc} err={err.strip()[:160]}")
            # Confirm via REST that it is actually gone (CLI delete resolved the id).
            check("function gone after CLI delete (404)",
                  c.status("GET", f"/api/v1/functions/{fid}") == 404)
            fid = None
    finally:
        cleanup(c)
    return summary()


def _try_deploy_invoke(cli):
    """Best-effort: deploy a tiny node function to its OWN name via the CLI and
    invoke it. Returns None when the deploy clearly couldn't build (sandbox
    missing), else a dict describing what happened. Uses a distinct name so it
    never collides with the REST-created NAME used elsewhere."""
    dep_name = DEPLOY_NAME
    src = None
    try:
        src = tempfile.mkdtemp(prefix="orva-cli-e2e-")
        with open(os.path.join(src, "handler.js"), "w") as f:
            f.write("export default async function () { return { ok: true }; }\n")

        rc, out, err = cli.run("deploy", src, "--name", dep_name,
                               "--runtime", "node", "--follow", timeout=120)
        blob = (out + err)
        deployed = ('Build succeeded.' in blob or '"status": "succeeded"' in blob
                    or '"status": "deployed"' in blob or '"status": "active"' in blob)
        # A build/sandbox failure (no nsjail) shows up as nonzero exit or a
        # non-success status. Treat anything that isn't a clear success as a
        # SKIP of just these two checks — the read-only CLI checks still stand.
        if rc != 0 or not deployed:
            _cli_delete(cli, dep_name)
            return {"failed": True, "detail": f"deploy rc={rc}: {blob.strip()[-300:]}"}

        result = {"detail": blob.strip()[-200:]}
        # invoke now prints the response BODY to stdout (status/timing go to
        # stderr); a 2xx invocation exits 0 with a non-empty body.
        irc, iout, ierr = cli.run("invoke", dep_name, "--body", "{}", timeout=60)
        try:
            invoke_body = json.loads(iout)
        except (TypeError, json.JSONDecodeError):
            invoke_body = None
        result["invoked"] = (irc == 0 and invoke_body == {"ok": True})
        result["invoke_detail"] = (iout + ierr).strip()[-200:]
        _cli_delete(cli, dep_name)
        return result
    except Exception as e:
        # Any harness/timeout error here means we can't validate deploy — skip it.
        try:
            _cli_delete(cli, dep_name)
        except Exception:
            pass
        return {"failed": True, "detail": f"deploy/invoke exception: {e}"}
    finally:
        if src and os.path.isdir(src):
            for root, _dirs, files in os.walk(src, topdown=False):
                for fn in files:
                    try:
                        os.remove(os.path.join(root, fn))
                    except OSError:
                        pass
                try:
                    os.rmdir(root)
                except OSError:
                    pass


def _cli_delete(cli, name):
    try:
        cli.run("functions", "delete", name, "--yes", timeout=30)
    except Exception:
        pass


if __name__ == "__main__":
    sys.exit(main())
