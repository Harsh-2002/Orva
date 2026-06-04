#!/usr/bin/env python3
"""CLI ↔ AI: `orva chat` one-shot drives the real agentic loop via the mock LLM.

The `chat` command talks to the same AI SSE backend the dashboard uses, over the
CLI's API key. Driven keyless through the mock provider (see harness), this covers
the one-shot (-p) path end to end: a plain reply, a read tool auto-running, a write
tool failing closed without approval, and the same write succeeding with
--auto-approve (which exercises the approve continuation stream). The interactive
REPL needs a TTY, so it isn't driven here; one-shot is the scriptable surface.
"""
import sys

from harness import (
    OrvaClient, CLIRunner, start_mock, configure_mock_provider,
    remove_mock_provider, section, check, summary, skip,
)

TEST_FN = "e2e-cli-chat-fn"

CREATE_ARGS = (
    'CALL create_function {"name":"%s","description":"e2e cli chat",'
    '"runtime":"node","entrypoint":"handler.js","timeout_ms":30000,'
    '"memory_mb":128,"cpus":1,"network_mode":"none","auth_mode":"none"}'
) % TEST_FN


def _functions(c):
    # A fresh instance returns {"functions": null}; `.get(..., [])` would yield
    # None (the key exists), so coalesce with `or []`.
    return ((c.get("/api/v1/functions") or {}).get("functions")) or []


def cleanup_fn(c):
    try:
        for fn in _functions(c):
            if fn.get("name") == TEST_FN:
                c.req("DELETE", f"/api/v1/functions/{fn['id']}", expect=(200, 204, 404))
    except Exception:
        pass


def fn_exists(c):
    return any(f.get("name") == TEST_FN for f in _functions(c))


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cli = CLIRunner()
    if not cli.available():
        return skip("orva binary not built (set ORVA_BIN / run `make build`)")

    start_mock()
    configure_mock_provider(c, approval="all_writes")
    cleanup_fn(c)
    try:
        section("orva chat -p plain reply")
        rc, out, err = cli.run("chat", "-p", "hello there", timeout=60)
        check("plain chat -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:200]}")
        check("plain chat prints a reply on stdout", out.strip() != "", out.strip()[:160])

        section("orva chat -p read tool auto-runs (list_functions)")
        rc, out, err = cli.run("chat", "-p", "CALL list_functions {}", timeout=60)
        check("read-tool chat -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:200]}")
        check("tool status line shown on stderr", "list_functions" in err, err.strip()[:200])

        section("orva chat -p write tool fails closed without --auto-approve")
        cleanup_fn(c)
        rc, out, err = cli.run("chat", "-p", CREATE_ARGS, timeout=60)
        check("write tool, no approval -> nonzero exit", rc != 0, f"rc={rc}")
        blob = (out + err).lower()
        check("error explains approval is required",
              "requires approval" in blob or "auto-approve" in blob, (out + err).strip()[:200])
        check("function NOT created on fail-closed", not fn_exists(c))

        section("orva chat -p --auto-approve runs the write tool")
        rc, out, err = cli.run("chat", "-p", CREATE_ARGS, "--auto-approve", timeout=90)
        check("auto-approve chat -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:200]}")
        check("function created via --auto-approve", fn_exists(c))

        section("orva chat --no-color emits no ANSI on stdout")
        rc, out, err = cli.run("chat", "-p", "hello", "--no-color", timeout=60)
        check("no-color chat -> exit 0", rc == 0, f"rc={rc} err={err.strip()[:200]}")
        check("no ANSI escapes on stdout", "\x1b" not in out, repr(out[:80]))
    finally:
        cleanup_fn(c)
        remove_mock_provider(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
