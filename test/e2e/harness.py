#!/usr/bin/env python3
"""
harness.py — shared helpers for Orva's comprehensive programmatic E2E suite.

Stdlib only (no pip). Provides:
  - OrvaClient: HTTP client for the whole /api/v1 surface (JSON + SSE streaming).
  - CLIRunner: runs the Orva binary's CLI subcommands against the same instance
    (the binary is server + CLI; we test both fronts speak to one backend).
  - mock-LLM helpers: point an Orva "openai" provider at the local mock so the
    AI agentic loop is testable end-to-end with NO real key.
  - a tiny check/section/summary report framework: each test file is its own
    process; run.py aggregates exit codes + stdout into CHECKLIST.md.

Config via env (run.py sets these for the isolated container):
  ORVA_URL      default http://127.0.0.1:8443     (the instance under test)
  ORVA_API_KEY  required                          (admin key of that instance)
  ORVA_BIN      default ../../build/orva          (CLI binary for CLIRunner)
  MOCK_PORT     default 11434                      (host port the mock listens on)
  MOCK_HOST     default 127.0.0.1                  (how the instance reaches the
                                                    mock; host.docker.internal in
                                                    a container)
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

import mock_llm

ORVA_URL = os.environ.get("ORVA_URL", "http://127.0.0.1:8443").rstrip("/")
ORVA_API_KEY = os.environ.get("ORVA_API_KEY", "")
ORVA_BIN = os.environ.get("ORVA_BIN", os.path.join(os.path.dirname(__file__), "..", "..", "build", "orva"))
MOCK_PORT = int(os.environ.get("MOCK_PORT", "11434"))
MOCK_HOST = os.environ.get("MOCK_HOST", "127.0.0.1")

# ── result tracking (process-local) ───────────────────────────────────────────

_PASS = 0
_FAIL = 0
_FAILURES = []


def section(title):
    print(f"\n\033[1;36m== {title} ==\033[0m")


def check(name, cond, detail=""):
    global _PASS, _FAIL
    if cond:
        _PASS += 1
        print(f"  \033[32m✓\033[0m {name}")
    else:
        _FAIL += 1
        _FAILURES.append(name + (f" — {detail}" if detail else ""))
        print(f"  \033[31m✗\033[0m {name}" + (f"  ({detail})" if detail else ""))
    return cond


def summary():
    print(f"\n{'-' * 50}")
    if _FAIL == 0:
        print(f"\033[32mALL PASSED — {_PASS} checks\033[0m")
    else:
        print(f"\033[31m{_FAIL} FAILED / {_PASS + _FAIL} checks\033[0m")
        for f in _FAILURES:
            print(f"  \033[31m✗\033[0m {f}")
    # Machine-readable trailer the orchestrator parses for the checklist.
    print(f"RESULT pass={_PASS} fail={_FAIL}")
    return 0 if _FAIL == 0 else 1


def skip(reason):
    """Mark a whole file as skipped (e.g. capability missing). Exit code 3."""
    print(f"\033[33mSKIP — {reason}\033[0m")
    print("RESULT pass=0 fail=0 skip=1")
    return 3


# ── HTTP client ────────────────────────────────────────────────────────────────

class OrvaClient:
    def __init__(self, base_url=ORVA_URL, api_key=ORVA_API_KEY):
        self.base = base_url.rstrip("/")
        self.key = api_key

    def _headers(self):
        h = {"Content-Type": "application/json"}
        if self.key:
            h["X-Orva-API-Key"] = self.key
        return h

    def req(self, method, path, body=None, expect=(200, 201, 204)):
        """Returns (status_code, parsed_json_or_text). Raises if status not in
        expect (pass expect=range(200,599) to inspect any status)."""
        data = json.dumps(body).encode() if body is not None else None
        r = urllib.request.Request(self.base + path, data=data, method=method, headers=self._headers())
        try:
            with urllib.request.urlopen(r, timeout=60) as resp:
                raw, code = resp.read(), resp.status
        except urllib.error.HTTPError as e:
            raw, code = e.read(), e.code
        if expect and code not in expect:
            raise AssertionError(f"{method} {path} -> {code}: {raw[:400]!r}")
        if not raw:
            return code, None
        try:
            return code, json.loads(raw)
        except Exception:
            return code, raw.decode(errors="replace")

    def get(self, path):
        return self.req("GET", path)[1]

    def post(self, path, body):
        return self.req("POST", path, body)[1]

    def put(self, path, body):
        return self.req("PUT", path, body)[1]

    def delete(self, path):
        return self.req("DELETE", path, expect=(200, 204))[0]

    def status(self, method, path, body=None):
        return self.req(method, path, body, expect=range(100, 600))[0]


def latest_execution_stderr(client, function_id="", function_name="", timeout=5):
    """Return persisted stderr for the newest execution of a function.

    Worker-start failures are deliberately not echoed in public invoke
    responses, but authenticated execution logs retain the diagnostic. E2E
    callers use this before cleanup so CI annotations identify the actual
    sandbox/runtime failure instead of reporting only WORKER_CRASHED.
    """
    if not function_id and function_name:
        code, payload = client.req("GET", "/api/v1/functions?limit=10000",
                                   expect=range(200, 599))
        if code == 200 and isinstance(payload, dict):
            for function in payload.get("functions") or []:
                if function.get("name") == function_name:
                    function_id = function.get("id", "")
                    break
    if not function_id:
        return ""

    deadline = time.time() + timeout
    while time.time() < deadline:
        code, payload = client.req(
            "GET", f"/api/v1/executions?function_id={function_id}&limit=10",
            expect=range(200, 599),
        )
        if code == 200 and isinstance(payload, dict):
            for execution in payload.get("executions") or []:
                execution_id = execution.get("id")
                if not execution_id:
                    continue
                log_code, logs = client.req(
                    "GET", f"/api/v1/executions/{execution_id}/logs",
                    expect=range(200, 599),
                )
                if log_code == 200 and isinstance(logs, dict) and logs.get("stderr"):
                    return str(logs["stderr"]).strip()
        time.sleep(0.2)
    return ""

    # SSE: POST and collect frames as (event, data) until the stream ends.
    def stream(self, path, body, timeout=90):
        data = json.dumps(body).encode()
        r = urllib.request.Request(self.base + path, data=data, method="POST", headers=self._headers())
        frames = []
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            event, datalines = None, []
            for raw in resp:
                line = raw.decode(errors="replace").rstrip("\r\n")
                if line == "":
                    if event is not None:
                        payload = "\n".join(datalines)
                        try:
                            payload = json.loads(payload) if payload else {}
                        except Exception:
                            pass
                        frames.append((event, payload))
                    event, datalines = None, []
                    continue
                if line.startswith("event:"):
                    event = line[6:].strip()
                elif line.startswith("data:"):
                    datalines.append(line[5:].strip())
        return frames

    def chat(self, content, conversation_id=None):
        body = {"content": content}
        if conversation_id:
            body["conversation_id"] = conversation_id
        frames = self.stream("/api/v1/ai/chat", body)
        conv = conversation_id or _first(frames, "conversation", "id") or _first(frames, "done", "conversation_id")
        return frames, conv

    def approve(self, row_id):
        return self.stream(f"/api/v1/ai/tool-calls/{row_id}/approve", {})

    def reject(self, row_id):
        return self.stream(f"/api/v1/ai/tool-calls/{row_id}/reject", {})

    def wait_healthy(self, timeout=30):
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.status("GET", "/api/v1/system/health") == 200:
                    return True
            except Exception:
                pass
            time.sleep(0.5)
        return False


# ── CLI runner ───────────────────────────────────────────────────────────────

class CLIRunner:
    """Runs `orva <args> --endpoint <url> --api-key <key>` and captures output.

    The binary is the same server+CLI build; --endpoint/--api-key are root
    persistent flags, so no ~/.orva/config.yaml is needed.
    """

    def __init__(self, binary=ORVA_BIN, base_url=ORVA_URL, api_key=ORVA_API_KEY):
        self.binary = binary
        self.base = base_url
        self.key = api_key

    def available(self):
        return bool(self.binary) and os.path.exists(self.binary)

    def run(self, *args, input_text=None, timeout=60):
        cmd = [self.binary, *args, "--endpoint", self.base]
        if self.key:
            cmd += ["--api-key", self.key]
        kwargs = {"capture_output": True, "text": True, "timeout": timeout}
        if input_text is None:
            # Never inherit a developer terminal: one-shot CLI tests must have
            # the same non-interactive stdin locally and on GitHub runners.
            kwargs["stdin"] = subprocess.DEVNULL
        else:
            kwargs["input"] = input_text
        try:
            p = subprocess.run(cmd, **kwargs)
            return p.returncode, p.stdout, p.stderr
        except subprocess.TimeoutExpired as exc:
            out = exc.stdout.decode(errors="replace") if isinstance(exc.stdout, bytes) else (exc.stdout or "")
            err = exc.stderr.decode(errors="replace") if isinstance(exc.stderr, bytes) else (exc.stderr or "")
            err += f"\ncommand timed out after {timeout}s"
            return 124, out, err


# ── frame helpers ──────────────────────────────────────────────────────────────

def events_of(frames, name):
    return [d for (e, d) in frames if e == name]


def has_event(frames, name):
    return any(e == name for (e, _) in frames)


def _first(frames, name, key):
    for e, d in frames:
        if e == name and isinstance(d, dict):
            return d.get(key)
    return None


def first_tool_call(frames):
    evs = events_of(frames, "tool_call")
    return evs[0] if evs else None


# ── mock LLM lifecycle ───────────────────────────────────────────────────────────

def start_mock(port=MOCK_PORT):
    httpd = mock_llm.serve(port)
    time.sleep(0.2)
    return httpd


def configure_mock_provider(client, port=MOCK_PORT, model="gpt-4o", approval="all_writes", thinking="off", api_key="test"):
    """Point an Orva 'openai' provider at the mock and select it. Uses MOCK_HOST
    so the instance (possibly in a container) can reach the host's mock. Snapshots
    the existing settings so remove_mock_provider can restore them — this keeps
    the shared singleton settings row stable across test modules (avoids the
    'defaults' check in test_ai_settings failing because an earlier module set a
    different provider)."""
    # Snapshot the ORIGINAL settings only once per client: a test may call
    # configure_mock_provider several times (different approval policies), and
    # re-saving each time would capture an already-mutated state, so the final
    # restore would leave the wrong provider behind for later modules.
    if getattr(client, "_saved_settings", "unset") == "unset":
        try:
            client._saved_settings = (client.get("/api/v1/ai/settings") or {}).get("settings")
        except Exception:
            client._saved_settings = None
    client.post("/api/v1/ai/providers", {
        "provider": "openai", "label": "e2e-mock", "api_key": api_key,
        "base_url": f"http://{MOCK_HOST}:{port}/v1", "enabled": True,
    })
    client.put("/api/v1/ai/settings", {
        "provider": "openai", "model": model, "thinking_level": thinking,
        "approval_policy": approval, "max_tool_iterations": 10,
    })


def remove_mock_provider(client):
    for p in (client.get("/api/v1/ai/providers") or {}).get("providers", []):
        if p.get("provider") == "openai" and p.get("label") == "e2e-mock":
            client.delete(f"/api/v1/ai/providers/{p['id']}")
    saved = getattr(client, "_saved_settings", None)
    if saved:
        try:
            client.put("/api/v1/ai/settings", saved)
        except Exception:
            pass
