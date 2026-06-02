#!/usr/bin/env python3
"""
mock_llm.py — a deterministic, OpenAI-compatible chat-completions server for
testing Orva's AI assistant end-to-end WITHOUT a real provider or API key.

Orva's embedded Bifrost gateway can point any provider at a custom base_url, so
a test configures an Orva provider like:

    provider = "openai", api_key = "test", base_url = "http://127.0.0.1:<port>/v1"

and then drives /api/v1/ai/chat. This server answers POST /v1/chat/completions
with streamed (SSE) OpenAI-format chunks. What it returns is driven entirely by
the conversation the agent sends, so tests are deterministic:

  - If the message history contains a tool result (role == "tool"), the model's
    "next turn" is a FINAL text answer (the loop terminates).
  - Otherwise, the last user message acts as a directive:
      "CALL <tool_name> <json-args>"  -> stream a single tool_call for that tool
      "CALL2 <toolA> <argsA> || <toolB> <argsB>" -> stream TWO tool calls at once
      anything else                   -> stream a short plain-text reply

This lets a test say content='CALL list_functions {}' and watch the agent
actually dispatch list_functions against the real Orva instance, feed the result
back, and get a final answer — the whole agentic loop, deterministically.

Stdlib only (no pip). Run standalone:  python3 mock_llm.py [port]
"""
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def _chunk(obj):
    return ("data: " + json.dumps(obj) + "\n\n").encode()


class Handler(BaseHTTPRequestHandler):
    # Silence the default per-request stderr logging (keeps test output clean).
    def log_message(self, *_args):
        pass

    def _read_body(self):
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length == 0:
            return {}
        try:
            return json.loads(self.rfile.read(length) or b"{}")
        except Exception:
            return {}

    def do_GET(self):
        # A trivial health/models endpoint so anything probing the base URL is happy.
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"object":"list","data":[{"id":"gpt-4o","object":"model"}]}')

    def do_POST(self):
        # Accept both /v1/chat/completions and /chat/completions.
        if not self.path.endswith("/chat/completions"):
            self.send_response(404)
            self.end_headers()
            return
        body = self._read_body()
        messages = body.get("messages", []) or []

        has_tool_result = any((m or {}).get("role") == "tool" for m in messages)
        last_user = ""
        for m in messages:
            if (m or {}).get("role") == "user":
                c = m.get("content")
                last_user = c if isinstance(c, str) else json.dumps(c)

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()

        try:
            if has_tool_result:
                self._stream_text("Done. The tool ran and returned a result.")
            elif last_user.strip().startswith("CALL2 "):
                self._stream_tool_calls(self._parse_multi(last_user.strip()[6:]))
            elif last_user.strip().startswith("CALL "):
                name, args = self._parse_call(last_user.strip()[5:])
                self._stream_tool_calls([(name, args)])
            else:
                self._stream_text("Hello from the mock model. You said: " + last_user[:200])
        except (BrokenPipeError, ConnectionResetError):
            pass

    # ── directive parsing ────────────────────────────────────────────────

    @staticmethod
    def _parse_call(s):
        s = s.strip()
        sp = s.find(" ")
        if sp < 0:
            return s, "{}"
        name = s[:sp].strip()
        args = s[sp + 1:].strip() or "{}"
        return name, args

    def _parse_multi(self, s):
        out = []
        for part in s.split("||"):
            part = part.strip()
            if part:
                out.append(self._parse_call(part))
        return out

    # ── OpenAI SSE emitters ──────────────────────────────────────────────

    def _w(self, data):
        self.wfile.write(data)
        self.wfile.flush()

    def _stream_text(self, text):
        self._w(_chunk({"id": "mock", "object": "chat.completion.chunk",
                        "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}]}))
        # Emit a few content deltas so streaming is exercised.
        for piece in _split(text):
            self._w(_chunk({"id": "mock", "object": "chat.completion.chunk",
                            "choices": [{"index": 0, "delta": {"content": piece}, "finish_reason": None}]}))
        self._w(_chunk({"id": "mock", "object": "chat.completion.chunk",
                        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                        "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}))
        self._w(b"data: [DONE]\n\n")

    def _stream_tool_calls(self, calls):
        # First chunk announces the assistant role.
        self._w(_chunk({"id": "mock", "object": "chat.completion.chunk",
                        "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}]}))
        for i, (name, args) in enumerate(calls):
            # Open the tool call (id + name), then stream the arguments.
            self._w(_chunk({"id": "mock", "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": {
                "tool_calls": [{"index": i, "id": f"call_{i}", "type": "function",
                                "function": {"name": name, "arguments": ""}}]}, "finish_reason": None}]}))
            self._w(_chunk({"id": "mock", "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": {
                "tool_calls": [{"index": i, "function": {"arguments": args}}]}, "finish_reason": None}]}))
        self._w(_chunk({"id": "mock", "object": "chat.completion.chunk",
                        "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}],
                        "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}))
        self._w(b"data: [DONE]\n\n")


def _split(text, n=24):
    return [text[i:i + n] for i in range(0, len(text), n)] or [""]


def serve(port, bind=None):
    # Bind 0.0.0.0 by default so an Orva container can reach the mock via
    # host.docker.internal; override with MOCK_BIND for stricter setups.
    import os as _os
    bind = bind or _os.environ.get("MOCK_BIND", "0.0.0.0")
    httpd = ThreadingHTTPServer((bind, port), Handler)
    t = threading.Thread(target=httpd.serve_forever, daemon=True)
    t.start()
    return httpd


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 11434
    print(f"mock LLM listening on http://127.0.0.1:{port}/v1")
    serve(port)
    try:
        threading.Event().wait()
    except KeyboardInterrupt:
        pass
