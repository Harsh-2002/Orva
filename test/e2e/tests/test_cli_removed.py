#!/usr/bin/env python3
"""The CLI no longer exposes the dashboard AI chat as a terminal command."""
import re
import subprocess
import sys

from harness import CLIRunner, OrvaClient, check, section, skip, summary


def main():
    client = OrvaClient()
    if not client.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2

    cli = CLIRunner()
    if not cli.available():
        return skip("orva binary not built (set ORVA_BIN / run `make build`)")

    # These checks are offline: API flags after --help confuse Cobra's parser.
    def run(*args):
        result = subprocess.run(
            [cli.binary, *args], stdin=subprocess.DEVNULL,
            capture_output=True, text=True, timeout=30,
        )
        return result.returncode, result.stdout, result.stderr

    section("chat is absent from CLI help")
    rc, out, err = run("--help")
    check("help command succeeds", rc == 0, f"rc={rc} err={err.strip()[:200]}")
    check("chat is not listed as a command",
          re.search(r"^\s+chat\s+", out, re.MULTILINE) is None, out[:500])

    section("removed chat command is rejected")
    rc, out, err = run("chat")
    check("chat exits nonzero", rc != 0, f"rc={rc}")
    check("chat reports unknown command",
          'unknown command "chat"' in err, err.strip()[:200])

    section("chat is absent from top-level completion")
    rc, out, err = run("__complete", "")
    check("completion query succeeds", rc == 0, f"rc={rc} err={err.strip()[:200]}")
    check("chat is not suggested",
          not any(line.split("\t", 1)[0].strip() == "chat" for line in out.splitlines()), out[:500])
    return summary()


if __name__ == "__main__":
    sys.exit(main())
