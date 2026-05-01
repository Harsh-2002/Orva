// Function templates — real-world starters for Python and Node. Each
// entry must be valid, copy-pasteable code that respects Orva's handler
// contract: a default-export `handler(event) -> {statusCode, headers, body}`.
//
// Categories (for the picker UX): "Starter", "Webhooks", "Auth",
// "Utility", "Scheduled". Templates that pair well with cron triggers
// flag `cron: true` so the deploy modal can pre-fill a daily expression.

// ─────────────────────────────────────────────────────────────────────
//  Python
// ─────────────────────────────────────────────────────────────────────

const py_http_hello = `import json


def handler(event):
    body = event.get("body") or {}
    if isinstance(body, str):
        body = json.loads(body) if body else {}
    name = body.get("name", "World") if isinstance(body, dict) else "World"

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"message": f"Hello {name}!", "language": "Python"}),
    }
`

const py_stripe_webhook = `# Verify a Stripe webhook and branch on event type.
# Set the secret as STRIPE_WEBHOOK_SECRET in the function's secrets so
# rotation doesn't require a redeploy.
import hashlib
import hmac
import json
import os
import time


def _verify(payload: bytes, header: str, secret: str, tolerance: int = 300) -> bool:
    # Stripe sends a header like: t=1614265978,v1=abcdef...
    parts = dict(p.split("=", 1) for p in header.split(",") if "=" in p)
    ts = int(parts.get("t", "0"))
    sig = parts.get("v1", "")
    if abs(time.time() - ts) > tolerance:
        return False
    signed = f"{ts}.{payload.decode('utf-8')}".encode()
    expected = hmac.new(secret.encode(), signed, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, sig)


def handler(event):
    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    sig_header = headers.get("stripe-signature", "")
    secret = os.environ.get("STRIPE_WEBHOOK_SECRET", "")
    raw = (event.get("body") or "").encode("utf-8") if isinstance(event.get("body"), str) else json.dumps(event.get("body") or {}).encode()

    if not _verify(raw, sig_header, secret):
        return {"statusCode": 401, "body": "invalid signature"}

    payload = json.loads(raw)
    event_type = payload.get("type", "unknown")

    # Branch on the event types you care about. Anything else is ack'd 200
    # so Stripe stops retrying.
    if event_type == "checkout.session.completed":
        session_id = payload["data"]["object"]["id"]
        # TODO: fulfil the order — record session_id in your DB.
        print(f"checkout completed: {session_id}")
    elif event_type == "customer.subscription.deleted":
        sub_id = payload["data"]["object"]["id"]
        print(f"subscription cancelled: {sub_id}")

    return {"statusCode": 200, "body": json.dumps({"received": True, "type": event_type})}
`

const py_github_webhook = `# Verify a GitHub webhook (HMAC-SHA256) and route on X-GitHub-Event.
# Set the secret as GITHUB_WEBHOOK_SECRET in the function's secrets.
import hashlib
import hmac
import json
import os


def handler(event):
    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    sig = headers.get("x-hub-signature-256", "")
    if not sig.startswith("sha256="):
        return {"statusCode": 401, "body": "missing signature"}

    secret = os.environ.get("GITHUB_WEBHOOK_SECRET", "")
    raw = event.get("body") or ""
    if not isinstance(raw, str):
        raw = json.dumps(raw)
    expected = "sha256=" + hmac.new(secret.encode(), raw.encode(), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, sig):
        return {"statusCode": 401, "body": "bad signature"}

    gh_event = headers.get("x-github-event", "ping")
    payload = json.loads(raw) if raw else {}

    if gh_event == "ping":
        return {"statusCode": 200, "body": json.dumps({"pong": True})}
    if gh_event == "push":
        ref = payload.get("ref", "")
        commits = len(payload.get("commits", []))
        print(f"push to {ref}: {commits} commit(s)")
    elif gh_event == "pull_request":
        action = payload.get("action", "")
        pr_num = payload.get("number", 0)
        print(f"PR #{pr_num} {action}")

    return {"statusCode": 200, "body": json.dumps({"event": gh_event})}
`

const py_slack_bot = `# Slack slash command handler. Slack POSTs application/x-www-form-urlencoded
# to the request URL and expects a JSON response within 3 seconds. Set
# SLACK_SIGNING_SECRET in the function's secrets to verify requests.
import hashlib
import hmac
import json
import os
import time
import urllib.parse


def _verify(ts: str, body: str, sig: str, secret: str) -> bool:
    if abs(time.time() - int(ts or "0")) > 300:
        return False
    base = f"v0:{ts}:{body}".encode()
    expected = "v0=" + hmac.new(secret.encode(), base, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, sig)


def handler(event):
    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    body = event.get("body") or ""
    if not isinstance(body, str):
        body = urllib.parse.urlencode(body)

    secret = os.environ.get("SLACK_SIGNING_SECRET", "")
    if not _verify(headers.get("x-slack-request-timestamp", ""),
                   body,
                   headers.get("x-slack-signature", ""),
                   secret):
        return {"statusCode": 401, "body": "invalid signature"}

    form = dict(urllib.parse.parse_qsl(body))
    user = form.get("user_name", "?")
    text = form.get("text", "").strip()

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "response_type": "in_channel",
            "text": f":wave: Hello @{user}! You said: \\"{text or '(nothing)'}\\"",
        }),
    }
`

const py_csv_to_json = `# Convert a CSV upload to JSON. Accepts plain text body OR a multipart
# upload with field name "file". Stream-parses with csv.reader so a 5MB
# CSV doesn't blow the 6MB request body cap with intermediate copies.
import csv
import io
import json


def handler(event):
    body = event.get("body") or ""
    if not isinstance(body, str):
        body = json.dumps(body)

    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    ctype = headers.get("content-type", "")

    # Strip multipart wrapper if present. Real multipart parsing is more
    # involved; this covers the common "single file, plain CSV body"
    # case that simple uploaders produce.
    if ctype.startswith("multipart/"):
        marker = "\\r\\n\\r\\n"
        if marker in body:
            body = body.split(marker, 1)[1]
            tail = body.rfind("\\r\\n--")
            if tail > 0:
                body = body[:tail]

    reader = csv.reader(io.StringIO(body))
    rows = list(reader)
    if not rows:
        return {"statusCode": 400, "body": "empty CSV"}

    header_row, *data_rows = rows
    out = [dict(zip(header_row, r)) for r in data_rows]

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"count": len(out), "rows": out}),
    }
`

const py_cron_cleanup = `# Scheduled DB cleanup: delete rows older than RETENTION_DAYS.
# Pair this with a Schedules entry like "0 2 * * *" (daily at 02:00).
# Configure DB_DSN as a secret. The handler is idempotent so multiple
# fires within the same window are safe.
import json
import os
from datetime import datetime, timedelta, timezone

# Replace this with your actual DB driver; psycopg2/asyncpg/etc.
# import psycopg2


def handler(event):
    retention_days = int(os.environ.get("RETENTION_DAYS", "30"))
    cutoff = datetime.now(timezone.utc) - timedelta(days=retention_days)

    # Example DB call (commented — drop your driver in):
    # conn = psycopg2.connect(os.environ["DB_DSN"])
    # cur = conn.cursor()
    # cur.execute("DELETE FROM events WHERE created_at < %s", (cutoff,))
    # deleted = cur.rowcount
    # conn.commit()
    # conn.close()
    deleted = 0  # replace with real count

    headers = {k.lower(): v for k, v in (event.get("headers") or {}).items()}
    triggered_by = headers.get("x-orva-trigger", "manual")

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "trigger": triggered_by,
            "cutoff": cutoff.isoformat(),
            "deleted_rows": deleted,
        }),
    }
`

const py_rss_summarize = `# RSS / Atom summarizer. Fetches a feed and returns the latest N items
# as JSON. Works for both RSS 2.0 and Atom. Requires network_mode=egress
# on the function settings to reach the upstream feed.
import json
import os
import urllib.request
import xml.etree.ElementTree as ET


def _strip_ns(tag: str) -> str:
    return tag.split("}", 1)[1] if "}" in tag else tag


def handler(event):
    body = event.get("body") or {}
    if isinstance(body, str):
        body = json.loads(body) if body else {}

    feed_url = body.get("url") or os.environ.get("FEED_URL", "")
    if not feed_url:
        return {"statusCode": 400, "body": "url is required"}

    limit = int(body.get("limit", 10))

    with urllib.request.urlopen(feed_url, timeout=10) as resp:
        xml_bytes = resp.read()

    root = ET.fromstring(xml_bytes)
    items = []
    # RSS: <channel><item>...</item></channel>; Atom: <feed><entry>...</entry></feed>
    for el in root.iter():
        tag = _strip_ns(el.tag)
        if tag in ("item", "entry"):
            entry = {_strip_ns(c.tag): (c.text or "").strip() for c in el}
            items.append(entry)
            if len(items) >= limit:
                break

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"feed": feed_url, "count": len(items), "items": items}),
    }
`

const py_image_thumbnail = `# Resize an image to a thumbnail. Accepts {"image_b64": "...", "width": 200}
# and returns base64 PNG. Requires Pillow (add "Pillow" to requirements.txt).
import base64
import io
import json

from PIL import Image


def handler(event):
    body = event.get("body") or {}
    if isinstance(body, str):
        body = json.loads(body) if body else {}

    img_b64 = body.get("image_b64") or ""
    if not img_b64:
        return {"statusCode": 400, "body": "image_b64 is required"}

    width = int(body.get("width", 200))
    height = int(body.get("height", 0)) or None

    raw = base64.b64decode(img_b64)
    img = Image.open(io.BytesIO(raw))

    # Preserve aspect ratio when only width is given.
    if height is None:
        ratio = width / img.width
        height = int(img.height * ratio)
    img.thumbnail((width, height))

    out = io.BytesIO()
    img.save(out, format="PNG", optimize=True)
    out_b64 = base64.b64encode(out.getvalue()).decode()

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"thumbnail_b64": out_b64, "width": width, "height": height}),
    }
`

const py_image_thumbnail_deps = `Pillow>=10.0
`

// Showcase: a single function that uses *every* core Orva surface —
// HTML rendering, JSON API, orva.kv for state, orva.jobs for background
// enrichment, cron-triggered cleanup, secret-gated admin DELETE, custom
// route mounting, content negotiation, full status-code spectrum. The
// best starting point for "what can a single Orva function do?"
const py_guestbook = `"""
guestbook — Orva showcase template

A single function that demonstrates EVERY core Orva surface:

  • Server-rendered dark-mode HTML page with form + feed
  • JSON API with pagination, validation, and CORS
  • orva.kv for durable per-function state
  • orva.jobs to enqueue post-submit enrichment work back to itself
  • Cron-trigger handling for periodic cleanup of stale entries
  • Admin auth via a function secret (ADMIN_TOKEN) on destructive ops
  • Custom route mount under /guestbook/*

Setup steps after deploy:
  1. Set the ADMIN_TOKEN secret (Settings → Secrets) to enable DELETE.
  2. Create a custom route /guestbook/* → this function (Routes API).
  3. (optional) Schedule a daily cron via the Schedules page; the
     handler reads x-orva-trigger and runs the cleanup branch.

Endpoints (after the route is mounted):
  GET  /guestbook/                       → render the page
  POST /guestbook/                       → form submission, 303 back
  GET  /guestbook/api/submissions        → list with ?limit=&cursor=
  GET  /guestbook/api/submissions/<id>   → one entry
  POST /guestbook/api/submissions        → JSON body, 201
  DELETE /guestbook/api/submissions/<id> → admin only (Bearer ADMIN_TOKEN)
  GET  /guestbook/api/stats              → totals + last-24h count
"""

import html as _html
import json
import os
import re
import secrets
import time
from urllib.parse import parse_qs

from orva import kv, jobs

ROUTE_BASE = "/guestbook"
NAME_RE    = re.compile(r"^[\\w \\-\\.\\'\\"@]{1,40}$")
MAX_MSG    = 280
LIST_LIMIT = 200
RETENTION_DAYS = int(os.environ.get("RETENTION_DAYS", "30"))


# ── helpers ──────────────────────────────────────────────────────────

def _new_id():
    return f"sub:{time.time_ns()}:{secrets.token_hex(2)}"


def _now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _now_unix():
    return int(time.time())


def _json_response(status, body, headers=None):
    h = {"Content-Type": "application/json", "Cache-Control": "no-store",
         "Access-Control-Allow-Origin": "*"}
    if headers:
        h.update(headers)
    return {"statusCode": status, "headers": h, "body": body}


def _html_response(body, status=200):
    return {
        "statusCode": status,
        "headers": {"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store"},
        "body": body,
    }


def _read_body(event):
    body = event.get("body")
    ct = (event.get("headers") or {}).get("content-type", "").lower()
    if isinstance(body, dict):
        return body
    if not body:
        return {}
    if "application/json" in ct:
        try:
            return json.loads(body) if isinstance(body, str) else body
        except json.JSONDecodeError:
            return {}
    if "application/x-www-form-urlencoded" in ct or "multipart/form-data" in ct:
        if isinstance(body, str):
            return {k: v[0] for k, v in parse_qs(body, keep_blank_values=True).items()}
    if isinstance(body, str):
        try:
            return json.loads(body)
        except json.JSONDecodeError:
            return {}
    return {}


def _validate(data):
    name = (data.get("name") or "").strip()
    message = (data.get("message") or "").strip()
    if not name:
        return "", "", "name is required"
    if not NAME_RE.match(name):
        return "", "", "name must be <= 40 chars; letters / digits / @ . - _ ' \\" space only"
    if not message:
        return "", "", "message is required"
    if len(message) > MAX_MSG:
        return "", "", f"message must be <= {MAX_MSG} chars"
    return name, message, ""


def _save(name, message):
    sid = _new_id()
    record = {
        "id":      sid,
        "name":    name,
        "message": message,
        "ts":      _now_iso(),
        "ts_unix": _now_unix(),
    }
    kv.put(sid, record)
    counter = (kv.get("meta:counter", default=0) or 0) + 1
    kv.put("meta:counter", counter)

    # Hand off enrichment to the background queue. The same function
    # picks up the job (see _on_job_trigger) — no separate worker
    # function. The user's POST returns immediately.
    try:
        jobs.enqueue("guestbook", {"action": "enrich", "id": sid})
    except Exception as e:
        _log("warn", "jobs.enqueue failed", error=str(e))

    return record


def _unwrap(row):
    """orva.kv.list returns either a parsed dict OR a {key, value: <json
    string>} envelope depending on runtime. Handle both."""
    if isinstance(row, dict) and "key" in row and "value" in row:
        v = row["value"]
        if isinstance(v, str):
            try:
                return json.loads(v)
            except json.JSONDecodeError:
                return None
        return v
    return row


def _list(limit=LIST_LIMIT, cursor=None):
    rows = kv.list(prefix="sub:", limit=limit) or []
    parsed = [_unwrap(r) for r in rows]
    parsed = [r for r in parsed if isinstance(r, dict)]
    parsed = list(reversed(parsed))  # newest first
    if cursor:
        try:
            i = next(idx for idx, r in enumerate(parsed) if r.get("id") == cursor)
            parsed = parsed[i + 1:]
        except StopIteration:
            pass
    return parsed


def _log(level, msg, **fields):
    fields.update({"level": level, "msg": msg})
    print(json.dumps(fields))


# ── HTML page (inline CSS, no external deps) ────────────────────────

PAGE = """\\
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>guestbook · {count}</title>
<style>
  :root {{
    --bg: #0a0a0d; --surface: #15151b; --border: #25252e;
    --fg: #e5e7eb; --muted: #8b8d97; --accent: #a78bfa; --error: #f87171;
    --mono: ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, monospace;
    --sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
  }}
  * {{ box-sizing: border-box; }}
  html, body {{ background: var(--bg); color: var(--fg); margin: 0; }}
  body {{
    font-family: var(--sans); font-size: 14px; line-height: 1.55;
    -webkit-font-smoothing: antialiased; padding: 4rem 1.5rem 6rem;
  }}
  main {{ max-width: 36rem; margin: 0 auto; }}
  header {{ margin-bottom: 2.5rem; }}
  h1 {{
    font-family: var(--mono); font-size: 1.05rem; margin: 0 0 .25rem;
    font-weight: 600; letter-spacing: -.01em;
  }}
  h1 .accent {{ color: var(--accent); }}
  header p {{ color: var(--muted); margin: 0; font-size: 13px; }}
  form {{
    display: grid; gap: .6rem; margin-bottom: 2.5rem; padding: 1rem;
    background: var(--surface); border: 1px solid var(--border); border-radius: 8px;
  }}
  label {{
    font-family: var(--mono); font-size: 11px; color: var(--muted);
    letter-spacing: .04em; text-transform: uppercase;
  }}
  input, textarea {{
    width: 100%; background: var(--bg); color: var(--fg);
    border: 1px solid var(--border); border-radius: 6px;
    padding: .55rem .7rem; font-family: var(--sans); font-size: 14px;
    resize: vertical; transition: border-color .12s ease;
  }}
  input:focus, textarea:focus {{ outline: none; border-color: var(--accent); }}
  textarea {{ min-height: 4.5rem; max-height: 12rem; font-family: var(--mono); font-size: 13px; }}
  .row {{ display: flex; align-items: center; justify-content: space-between; gap: .75rem; }}
  .charcount {{ font-family: var(--mono); font-size: 11px; color: var(--muted); }}
  button {{
    font-family: var(--mono); font-size: 12px;
    background: var(--accent); color: #0a0a0d; border: 0;
    padding: .55rem 1rem; border-radius: 6px; cursor: pointer;
    font-weight: 600; letter-spacing: .02em; transition: filter .12s ease;
  }}
  button:hover {{ filter: brightness(1.08); }}
  .error {{ color: var(--error); font-size: 12px; margin: 0; font-family: var(--mono); }}
  ul.entries {{ list-style: none; margin: 0; padding: 0; }}
  .entries li {{ padding: 1rem 0; border-top: 1px solid var(--border); }}
  .entries li:first-child {{ border-top: 0; padding-top: 0; }}
  .entries .head {{
    display: flex; justify-content: space-between; gap: .75rem;
    align-items: baseline; margin-bottom: .25rem;
  }}
  .entries .name {{ font-weight: 600; color: var(--fg); }}
  .entries time {{ font-family: var(--mono); font-size: 11px; color: var(--muted); }}
  .entries .msg {{ color: var(--fg); white-space: pre-wrap; word-break: break-word; font-size: 13.5px; }}
  .empty {{ color: var(--muted); font-style: italic; text-align: center; padding: 2.5rem 0; }}
  footer {{
    margin-top: 3rem; padding-top: 1.5rem; border-top: 1px solid var(--border);
    color: var(--muted); font-size: 12px; font-family: var(--mono); text-align: center;
  }}
  footer code {{ color: var(--accent); }}
  a {{ color: var(--accent); }}
</style>
</head>
<body>
<main>
  <header>
    <h1>guestbook<span class="accent">.</span></h1>
    <p>Drop a note — say hi, share a link, leave a thought.</p>
  </header>

  <form method="POST" action="{base}/" autocomplete="off">
    <div>
      <label for="name">Name</label>
      <input id="name" name="name" maxlength="40" required value="{prefill_name}">
    </div>
    <div>
      <label for="message">Message</label>
      <textarea id="message" name="message" maxlength="{max_msg}" required>{prefill_msg}</textarea>
    </div>
    {error_html}
    <div class="row">
      <span class="charcount" id="cc">0 / {max_msg}</span>
      <button type="submit">Sign</button>
    </div>
  </form>

  {entries_html}

  <footer>
    <span>{count} {entry_word}</span>
    · <span>JSON at <code>{base}/api/submissions</code></span>
    · <span>Stats at <code>{base}/api/stats</code></span>
  </footer>
</main>
<script>
  (function () {{
    var ta = document.getElementById('message');
    var cc = document.getElementById('cc');
    function tick() {{ cc.textContent = ta.value.length + ' / {max_msg}'; }}
    ta.addEventListener('input', tick);
    tick();
  }})();
</script>
</body>
</html>
"""


def _render_page(entries, count, error="", prefill_name="", prefill_msg="", base=ROUTE_BASE):
    if entries:
        items = []
        for e in entries:
            items.append(
                "<li>"
                "<div class=\\"head\\">"
                f"<span class=\\"name\\">{_html.escape(str(e.get('name','')))}</span>"
                f"<time datetime=\\"{_html.escape(str(e.get('ts','')))}\\">{_html.escape(_pretty_time(str(e.get('ts',''))))}</time>"
                "</div>"
                f"<div class=\\"msg\\">{_html.escape(str(e.get('message','')))}</div>"
                "</li>"
            )
        entries_html = "<ul class=\\"entries\\">" + "".join(items) + "</ul>"
    else:
        entries_html = "<div class=\\"empty\\">No entries yet — be the first.</div>"

    error_html = f"<p class=\\"error\\">{_html.escape(error)}</p>" if error else ""

    return PAGE.format(
        count=count,
        entry_word="entry" if count == 1 else "entries",
        max_msg=MAX_MSG,
        prefill_name=_html.escape(prefill_name),
        prefill_msg=_html.escape(prefill_msg),
        error_html=error_html,
        entries_html=entries_html,
        base=base,
    )


def _pretty_time(iso):
    if not iso:
        return ""
    try:
        ts = time.mktime(time.strptime(iso, "%Y-%m-%dT%H:%M:%SZ"))
    except Exception:
        return iso
    delta = max(0, time.time() - ts)
    if delta < 45:        return "just now"
    if delta < 90:        return "1 min ago"
    if delta < 3600:      return f"{int(delta // 60)} min ago"
    if delta < 7200:      return "1 hour ago"
    if delta < 86400:     return f"{int(delta // 3600)} hours ago"
    if delta < 172800:    return "yesterday"
    return iso[:10]


def _strip_route_prefix(path):
    if path.startswith(ROUTE_BASE):
        rest = path[len(ROUTE_BASE):]
        return rest if rest else "/"
    if path.startswith("/fn/"):
        rest = path[len("/fn/"):]
        i = rest.find("/")
        return rest[i:] if i >= 0 else "/"
    return path


# ── trigger dispatch (cron / job) ───────────────────────────────────

def _on_cron_trigger(event):
    """Fired by the scheduler. Sweeps records older than RETENTION_DAYS."""
    cutoff = _now_unix() - RETENTION_DAYS * 86400
    deleted = 0
    rows = kv.list(prefix="sub:", limit=1000) or []
    for row in (_unwrap(r) for r in rows):
        if not isinstance(row, dict):
            continue
        if row.get("ts_unix", 0) < cutoff:
            kv.delete(row["id"])
            kv.delete(f"enriched:{row['id']}")
            deleted += 1
    _log("info", "cron cleanup",
         retention_days=RETENTION_DAYS, cutoff=cutoff, deleted=deleted)
    return _json_response(200, {"trigger": "cron", "deleted": deleted, "cutoff": cutoff})


def _on_job_trigger(event):
    """Fired by orva.jobs.enqueue. Computes enrichment after a
    submission lands so the user's POST returns instantly."""
    body = event.get("body") or {}
    if isinstance(body, str):
        try:
            body = json.loads(body)
        except json.JSONDecodeError:
            body = {}
    action = body.get("action")
    sid = body.get("id")
    if action != "enrich" or not sid:
        return _json_response(400, {"error": "unknown job action", "body": body})

    record = kv.get(sid)
    if not record:
        return _json_response(404, {"error": "submission not found", "id": sid})

    msg = record.get("message", "")
    enriched = {
        "id":      sid,
        "length":  len(msg),
        "tokens":  len(msg.split()),
        "links":   bool(re.search(r"https?://", msg)),
        "checked": _now_iso(),
    }
    kv.put(f"enriched:{sid}", enriched)
    _log("info", "job enriched", id=sid, length=enriched["length"], tokens=enriched["tokens"])
    return _json_response(200, {"trigger": "job", "enriched": enriched})


# ── admin auth ──────────────────────────────────────────────────────

def _is_admin(event):
    """Authorization: Bearer <ADMIN_TOKEN>. Token from a function secret."""
    token = os.environ.get("ADMIN_TOKEN", "")
    if not token:
        return False
    auth = (event.get("headers") or {}).get("authorization", "")
    if auth.startswith("Bearer "):
        return secrets.compare_digest(auth[7:], token)
    return False


# ── main dispatch ───────────────────────────────────────────────────

def handler(event):
    headers = event.get("headers") or {}
    trigger = headers.get("x-orva-trigger", "")

    # Cron / job invocations short-circuit. Same function, same KV
    # namespace — just a different entry point.
    if trigger == "cron":
        return _on_cron_trigger(event)
    if trigger == "job":
        return _on_job_trigger(event)

    method = (event.get("method") or "GET").upper()
    raw_path = (event.get("path") or "/").split("?", 1)[0]
    path = _strip_route_prefix(raw_path)
    parts = [p for p in path.split("/") if p]

    if method == "OPTIONS":
        return {
            "statusCode": 204,
            "headers": {
                "Access-Control-Allow-Origin": "*",
                "Access-Control-Allow-Methods": "GET, POST, DELETE, OPTIONS",
                "Access-Control-Allow-Headers": "Content-Type, Authorization",
                "Access-Control-Max-Age": "600",
            },
            "body": "",
        }

    # GET / → HTML
    if method == "GET" and not parts:
        entries = _list(limit=50)
        count = kv.get("meta:counter", default=0) or 0
        return _html_response(_render_page(entries, count))

    # POST / → form submission
    if method == "POST" and not parts:
        data = _read_body(event)
        name, message, err = _validate(data)
        if err:
            entries = _list(limit=50)
            count = kv.get("meta:counter", default=0) or 0
            return _html_response(_render_page(
                entries, count, error=err,
                prefill_name=str(data.get("name", "")),
                prefill_msg=str(data.get("message", "")),
            ), status=400)
        _save(name, message)
        return {"statusCode": 303, "headers": {"Location": ROUTE_BASE + "/"}, "body": ""}

    # ── JSON API ────────────────────────────────────────────────────
    if parts and parts[0] == "api":

        # GET /api/stats
        if method == "GET" and len(parts) == 2 and parts[1] == "stats":
            count = kv.get("meta:counter", default=0) or 0
            day_ago = _now_unix() - 86400
            recent = sum(1 for r in _list(limit=500) if r.get("ts_unix", 0) >= day_ago)
            return _json_response(200, {
                "total":         count,
                "last_24h":      recent,
                "retention_days": RETENTION_DAYS,
            })

        if len(parts) >= 2 and parts[1] == "submissions":
            # GET /api/submissions[?limit=&cursor=]
            if method == "GET" and len(parts) == 2:
                q = event.get("query", {}) or {}
                limit = min(int(q.get("limit", LIST_LIMIT)), LIST_LIMIT)
                cursor = q.get("cursor")
                items = _list(limit=limit, cursor=cursor)
                next_cursor = items[-1]["id"] if len(items) == limit else None
                return _json_response(200, {
                    "count":       kv.get("meta:counter", default=0) or 0,
                    "submissions": items,
                    "next_cursor": next_cursor,
                })

            # GET /api/submissions/<id>
            if method == "GET" and len(parts) == 3:
                row = kv.get(parts[2])
                if not row:
                    return _json_response(404, {"error": "not found"})
                enriched = kv.get(f"enriched:{parts[2]}")
                if enriched:
                    row = {**row, "enriched": enriched}
                return _json_response(200, row)

            # POST /api/submissions
            if method == "POST" and len(parts) == 2:
                data = _read_body(event)
                name, message, err = _validate(data)
                if err:
                    return _json_response(400, {"error": err})
                return _json_response(201, _save(name, message))

            # DELETE /api/submissions/<id>  (admin only)
            if method == "DELETE" and len(parts) == 3:
                if not _is_admin(event):
                    return _json_response(401, {"error": "admin token required"})
                kv.delete(parts[2])
                kv.delete(f"enriched:{parts[2]}")
                return _json_response(200, {"status": "deleted", "id": parts[2]})

    return _json_response(404, {"error": "not found", "path": path})
`

const py_stream_llm = `# Streaming LLM-style token generator (v0.4 C1 showcase).
#
# Yields one fake "token" every 100ms so curl --no-buffer renders the
# response progressively. Demonstrates the streaming protocol:
#
#   1. The first yield is an Orva-shaped dict { statusCode, headers }.
#      The adapter recognises this as the response head and emits a
#      'response_start' frame to the proxy.
#   2. Each subsequent yield becomes a 'chunk' frame; the proxy writes +
#      flushes those to the HTTP client over chunked transfer encoding.
#   3. When the generator returns, the adapter sends 'response_end' and
#      the connection closes cleanly.
#
# Replace the fake_tokens list + sleep with your real LLM SDK call (e.g.
# OpenAI's stream=True API or Anthropic's stream(): for a real backend
# you'd "yield" from the SDK's iterator). TTFB stays ~10ms because the
# first yield fires before any network I/O.
import time


def handler(event):
    fake_tokens = (
        "Streaming responses arrive token by token, just like the major "
        "LLM APIs. Each yield in this generator becomes one chunk on the "
        "wire — the HTTP client renders progress as soon as bytes leave "
        "the function. Try this with: curl --no-buffer http://localhost:8443/fn/<short_id>/"
    ).split()

    # First yield = response head. statusCode + headers establish the
    # connection; body is empty here because the words come as chunks.
    yield {
        "statusCode": 200,
        "headers": {
            "Content-Type": "text/plain; charset=utf-8",
            # X-Accel-Buffering disables nginx response buffering for
            # operators who put a reverse proxy in front of orvad.
            "X-Accel-Buffering": "no",
        },
    }

    for word in fake_tokens:
        yield word + " "
        time.sleep(0.1)
    yield "\\n"
`

// ─────────────────────────────────────────────────────────────────────
//  Node
// ─────────────────────────────────────────────────────────────────────

const node_http_hello = `exports.handler = async (event) => {
  const body = typeof event.body === 'string'
    ? JSON.parse(event.body || '{}')
    : event.body || {}
  const name = body.name || 'World'
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message: \`Hello \${name}!\`, language: 'Node.js' }),
  }
}
`

const node_discord_webhook = `// Discord interactions endpoint. Set DISCORD_PUBLIC_KEY in secrets.
// Discord requires Ed25519 signature verification within 3 seconds.
const nacl = require('tweetnacl')

exports.handler = async (event) => {
  const headers = Object.fromEntries(
    Object.entries(event.headers || {}).map(([k, v]) => [k.toLowerCase(), v])
  )
  const sig = headers['x-signature-ed25519']
  const ts  = headers['x-signature-timestamp']
  const publicKey = process.env.DISCORD_PUBLIC_KEY || ''

  const body = typeof event.body === 'string' ? event.body : JSON.stringify(event.body || {})

  if (!sig || !ts || !publicKey) {
    return { statusCode: 401, body: 'missing signature' }
  }
  const verified = nacl.sign.detached.verify(
    Buffer.from(ts + body),
    Buffer.from(sig, 'hex'),
    Buffer.from(publicKey, 'hex')
  )
  if (!verified) return { statusCode: 401, body: 'bad signature' }

  const payload = JSON.parse(body)

  // Type 1: PING (Discord's liveness probe).
  if (payload.type === 1) {
    return {
      statusCode: 200,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 1 }),
    }
  }

  // Type 2: APPLICATION_COMMAND (slash command).
  if (payload.type === 2) {
    const command = payload.data?.name || 'unknown'
    return {
      statusCode: 200,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 4, // CHANNEL_MESSAGE_WITH_SOURCE
        data: { content: \`Got /\${command}\` },
      }),
    }
  }

  return { statusCode: 200, body: JSON.stringify({ ok: true }) }
}
`

const node_discord_webhook_deps = `{
  "name": "discord-webhook",
  "version": "1.0.0",
  "dependencies": {
    "tweetnacl": "^1.0.3"
  }
}
`

const node_jwt_validator = `// Verify a JWT (HS256) and return its decoded claims. Set JWT_SECRET
// in secrets. The token comes in as Authorization: Bearer <token>.
const crypto = require('crypto')

const _b64urlDecode = (s) => Buffer.from(s.replace(/-/g, '+').replace(/_/g, '/'), 'base64')

exports.handler = async (event) => {
  const headers = Object.fromEntries(
    Object.entries(event.headers || {}).map(([k, v]) => [k.toLowerCase(), v])
  )
  const auth = headers['authorization'] || ''
  const token = auth.startsWith('Bearer ') ? auth.slice(7) : ''
  if (!token) return { statusCode: 401, body: 'missing bearer token' }

  const secret = process.env.JWT_SECRET || ''
  const [headerB64, payloadB64, sigB64] = token.split('.')
  if (!sigB64) return { statusCode: 401, body: 'malformed token' }

  const expected = crypto
    .createHmac('sha256', secret)
    .update(headerB64 + '.' + payloadB64)
    .digest('base64')
    .replace(/\\+/g, '-').replace(/\\//g, '_').replace(/=+$/, '')

  // timingSafeEqual requires equal-length buffers — bail before that
  // check on length mismatch so a short tampered signature can't crash
  // the function.
  if (expected.length !== sigB64.length ||
      !crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(sigB64))) {
    return { statusCode: 401, body: 'bad signature' }
  }

  const claims = JSON.parse(_b64urlDecode(payloadB64).toString())
  if (claims.exp && Date.now() / 1000 > claims.exp) {
    return { statusCode: 401, body: 'expired' }
  }

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ valid: true, claims }),
  }
}
`

const node_oauth_callback = `// OAuth 2.0 callback — exchanges an authorization code for an access
// token. Configure CLIENT_ID, CLIENT_SECRET, TOKEN_URL, REDIRECT_URI in
// secrets. Call this from \`/oauth/callback?code=...&state=...\`.
exports.handler = async (event) => {
  const url = new URL('http://x' + (event.path || '/'))
  const code = url.searchParams.get('code')
  if (!code) return { statusCode: 400, body: 'missing code' }

  const tokenURL = process.env.TOKEN_URL
  const params = new URLSearchParams({
    grant_type:    'authorization_code',
    code,
    redirect_uri:  process.env.REDIRECT_URI || '',
    client_id:     process.env.CLIENT_ID || '',
    client_secret: process.env.CLIENT_SECRET || '',
  })

  const res = await fetch(tokenURL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: params,
  })
  const data = await res.json()
  if (!res.ok) {
    return { statusCode: res.status, body: JSON.stringify(data) }
  }

  // In production: store data.access_token in your user's session
  // (Orva session cookie, KV, or DB). Returning it here is for the
  // template's transparency only — never echo tokens back to browsers
  // that didn't initiate the flow.
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ok: true,
      token_type: data.token_type,
      expires_in: data.expires_in,
    }),
  }
}
`

const node_md_to_html = `// Convert markdown to safe HTML using marked + DOMPurify-like escaping.
// Body: {"markdown": "# Hello\\n..."}.
const { marked } = require('marked')

const _escape = (s) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

exports.handler = async (event) => {
  const body = typeof event.body === 'string'
    ? JSON.parse(event.body || '{}')
    : event.body || {}
  const md = body.markdown || ''
  if (!md) return { statusCode: 400, body: 'markdown is required' }

  // marked sanitizes inline HTML when sanitize=true is set on options.
  // For belt-and-suspenders we also escape the input first when the
  // caller asks for it via opts.escape=true.
  const html = marked.parse(body.escape ? _escape(md) : md, { async: false })

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ html, length: html.length }),
  }
}
`

const node_md_to_html_deps = `{
  "name": "md-to-html",
  "version": "1.0.0",
  "dependencies": {
    "marked": "^12.0.0"
  }
}
`

const node_rest_proxy = `// REST proxy with auth header injection. Forwards every request to
// UPSTREAM_URL (env), adds an upstream auth token (UPSTREAM_TOKEN
// secret), and returns the upstream's response verbatim. Requires
// network_mode=egress.
exports.handler = async (event) => {
  const upstream = process.env.UPSTREAM_URL || ''
  const token    = process.env.UPSTREAM_TOKEN || ''
  if (!upstream) return { statusCode: 500, body: 'UPSTREAM_URL unset' }

  const path = event.path || '/'
  const url  = upstream.replace(/\\/$/, '') + path

  const fwdHeaders = { ...event.headers }
  delete fwdHeaders['host']
  delete fwdHeaders['x-orva-api-key']
  if (token) fwdHeaders['authorization'] = 'Bearer ' + token

  const init = { method: event.method || 'GET', headers: fwdHeaders }
  if (event.body && event.method !== 'GET' && event.method !== 'HEAD') {
    init.body = typeof event.body === 'string' ? event.body : JSON.stringify(event.body)
  }

  const res  = await fetch(url, init)
  const text = await res.text()
  const ct   = res.headers.get('content-type') || 'application/json'

  return { statusCode: res.status, headers: { 'Content-Type': ct }, body: text }
}
`

const node_email_digest = `// Daily email digest, fired by a Schedules entry like "0 9 * * *".
// Sends a summary email via SMTP. Configure SMTP_URL (full DSN with
// auth, e.g. smtps://user:pass@smtp.example.com:465) and DIGEST_TO in
// secrets. Requires network_mode=egress + nodemailer.
const nodemailer = require('nodemailer')

const buildSummary = async () => {
  // Replace with a real query against your data source — Postgres,
  // an internal API, the Orva KV store, etc.
  return {
    new_signups: 12,
    revenue_usd: 4280,
    tickets:     3,
  }
}

exports.handler = async (event) => {
  const transport = nodemailer.createTransport(process.env.SMTP_URL)
  const summary   = await buildSummary()

  const lines = Object.entries(summary).map(([k, v]) => \`  \${k}: \${v}\`).join('\\n')
  const body  = \`Daily digest — \${new Date().toISOString().slice(0, 10)}\\n\\n\${lines}\\n\`

  await transport.sendMail({
    from:    process.env.DIGEST_FROM || 'orva@localhost',
    to:      process.env.DIGEST_TO,
    subject: 'Daily digest',
    text:    body,
  })

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sent: true, summary }),
  }
}
`

const node_email_digest_deps = `{
  "name": "email-digest",
  "version": "1.0.0",
  "dependencies": {
    "nodemailer": "^6.9.0"
  }
}
`

const node_url_shortener = `// URL shortener using Orva's built-in KV store (Phase 3).
//   POST /        body: {"url": "https://..."} → {"slug": "ab12cd"}
//   GET  /<slug>  → 302 redirect to the stored URL
// Slugs live forever unless you set TTL_SECONDS.
const { kv } = require('orva')

const _slug = () => Math.random().toString(36).slice(2, 8)

exports.handler = async (event) => {
  if (event.method === 'POST') {
    const body = typeof event.body === 'string'
      ? JSON.parse(event.body || '{}')
      : event.body || {}
    if (!body.url) return { statusCode: 400, body: 'url is required' }

    const slug = body.slug || _slug()
    const ttl  = parseInt(process.env.TTL_SECONDS || '0', 10) || undefined
    await kv.put(\`url:\${slug}\`, body.url, { ttlSeconds: ttl })

    return {
      statusCode: 200,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ slug, url: body.url }),
    }
  }

  // GET /<slug>: look up + redirect.
  const slug = (event.path || '').replace(/^\\//, '').split('/')[0]
  if (!slug) return { statusCode: 404, body: 'not found' }

  const url = await kv.get(\`url:\${slug}\`)
  if (!url) return { statusCode: 404, body: 'unknown slug' }

  return { statusCode: 302, headers: { 'Location': url }, body: '' }
}
`

// ─────────────────────────────────────────────────────────────────────
//  TypeScript
// ─────────────────────────────────────────────────────────────────────

// Minimal typed handler. The build pipeline detects tsconfig.json and
// runs `npx --no-install tsc --project tsconfig.json` after `npm install`,
// so the deployed runtime entrypoint is actually `dist/handler.js` — but
// the user authors only TypeScript and the source-map / type-error
// reporting is what they expect.
const ts_hello = `// Minimal TypeScript handler. The Orva builder compiles this with tsc
// at deploy time; the runtime invokes the emitted dist/handler.js.
type OrvaEvent = {
  method?: string
  path?: string
  headers?: Record<string, string>
  body?: string | Record<string, unknown>
}

type OrvaResponse = {
  statusCode: number
  headers?: Record<string, string>
  body: string
}

const handler = async (event: OrvaEvent): Promise<OrvaResponse> => {
  const body =
    typeof event.body === 'string'
      ? (event.body ? JSON.parse(event.body) : {})
      : (event.body ?? {})
  const name = (body as { name?: string }).name ?? 'World'

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ok: true, runtime: 'typescript', message: \`Hello \${name}!\` }),
  }
}

export = handler
`

// Companion package.json — typescript declared as a dep so the build
// pipeline's verifyTypeScriptDeclared() is satisfied. Pin to ^5.4 so a
// future tsc release that breaks our default tsconfig doesn't silently
// blow up redeploys of saved snapshots.
const ts_hello_deps = `{
  "name": "ts-hello",
  "version": "1.0.0",
  "dependencies": {
    "typescript": "^5.4"
  }
}
`

// Minimal tsconfig — emits to ./dist (matches the builder's default
// outDir fallback) and targets a Node-compatible spec. CommonJS so
// `export = handler` matches the adapter's `require()`-based loader.
const ts_hello_tsconfig = `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "commonjs",
    "outDir": "./dist",
    "rootDir": "./",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true
  },
  "include": ["handler.ts"],
  "exclude": ["node_modules", "dist"]
}
`

// ─────────────────────────────────────────────────────────────────────
//  Manifest — one entry per template. Code/deps come from the constants
//  above. The Editor reads this list directly.
// ─────────────────────────────────────────────────────────────────────

const pythonTemplates = [
  { id: 'py-http-hello',     category: 'Starter',   label: 'HTTP Hello',
    description: 'Minimal POST /. Echoes a name from the JSON body.',
    code: py_http_hello, deps: '' },

  { id: 'py-stripe-webhook', category: 'Webhooks',  label: 'Stripe webhook',
    description: 'Verify Stripe signature; branch on event.type.',
    code: py_stripe_webhook, deps: '' },

  { id: 'py-github-webhook', category: 'Webhooks',  label: 'GitHub webhook',
    description: 'HMAC-SHA256 verification; route on X-GitHub-Event.',
    code: py_github_webhook, deps: '' },

  { id: 'py-slack-bot',      category: 'Webhooks',  label: 'Slack slash command',
    description: 'Parse application/x-www-form-urlencoded; respond inline.',
    code: py_slack_bot, deps: '' },

  { id: 'py-csv-to-json',    category: 'Utility',   label: 'CSV → JSON',
    description: 'Parse a CSV upload and return a JSON array of rows.',
    code: py_csv_to_json, deps: '' },

  { id: 'py-cron-cleanup',   category: 'Scheduled', label: 'Scheduled DB cleanup', cron: true,
    description: 'Daily delete of rows older than RETENTION_DAYS.',
    code: py_cron_cleanup, deps: '' },

  { id: 'py-rss-summarize',  category: 'Utility',   label: 'RSS summarizer',
    description: 'Fetch an RSS / Atom feed and return latest N items.',
    code: py_rss_summarize, deps: '' },

  { id: 'py-image-thumbnail', category: 'Utility',  label: 'Image thumbnail',
    description: 'Resize a base64 image with Pillow; returns PNG.',
    code: py_image_thumbnail, deps: py_image_thumbnail_deps },

  { id: 'py-guestbook',      category: 'Showcase',  label: 'Guestbook (full-stack showcase)', cron: true,
    description: 'HTML page + JSON API + KV + jobs + cron + secrets in one file. Best demo of what one Orva function can do.',
    code: py_guestbook, deps: '' },

  { id: 'py-stream-llm',     category: 'Showcase',  label: 'Streaming LLM tokens',
    description: 'Generator that yields one word every 100ms. Demonstrates v0.4 chunked streaming end-to-end.',
    code: py_stream_llm, deps: '' },
]

const nodeTemplates = [
  { id: 'node-http-hello',      category: 'Starter',   label: 'HTTP Hello',
    description: 'Minimal POST /. Echoes a name from the JSON body.',
    code: node_http_hello, deps: '' },

  { id: 'node-discord-webhook', category: 'Webhooks',  label: 'Discord webhook',
    description: 'Ed25519 verify + slash command response.',
    code: node_discord_webhook, deps: node_discord_webhook_deps },

  { id: 'node-jwt-validator',   category: 'Auth',      label: 'JWT validator',
    description: 'Verify HS256 token from Authorization header.',
    code: node_jwt_validator, deps: '' },

  { id: 'node-oauth-callback',  category: 'Auth',      label: 'OAuth callback',
    description: 'Exchange authorization_code for an access token.',
    code: node_oauth_callback, deps: '' },

  { id: 'node-md-to-html',      category: 'Utility',   label: 'Markdown → HTML',
    description: 'Render markdown via marked; safe inline HTML.',
    code: node_md_to_html, deps: node_md_to_html_deps },

  { id: 'node-rest-proxy',      category: 'Utility',   label: 'REST proxy',
    description: 'Forward to upstream + inject Authorization header.',
    code: node_rest_proxy, deps: '' },

  { id: 'node-email-digest',    category: 'Scheduled', label: 'Email digest', cron: true,
    description: 'Send a daily summary via SMTP (nodemailer).',
    code: node_email_digest, deps: node_email_digest_deps },

  { id: 'node-url-shortener',   category: 'Utility',   label: 'URL shortener',
    description: 'POST creates a slug, GET redirects. Uses Orva KV.',
    code: node_url_shortener, deps: '' },

  // TypeScript starter — the builder's tsc step picks this up via the
  // companion tsconfig.json. The `extras` field carries auxiliary files
  // the Editor will eventually surface as additional tabs / drop into the
  // archive at deploy time. Today the Editor reads `code` + `deps` only;
  // `extras` is forward-compatible — additional fields are ignored, not
  // an error, so this entry won't break the existing template loader.
  { id: 'ts-hello',             category: 'Starter',   label: 'TypeScript hello',
    description: 'Typed handler compiled at deploy time via tsc. Outputs to dist/handler.js.',
    code: ts_hello, deps: ts_hello_deps,
    extras: { 'tsconfig.json': ts_hello_tsconfig },
    entrypoint: 'handler.ts' },
]

// Indexed by runtime — Editor.vue uses this directly.
export const templates = {
  python313: pythonTemplates,
  python314: pythonTemplates,
  node22:    nodeTemplates,
  node24:    nodeTemplates,
}

// Default code (the "HTTP Hello" template) per runtime — used when the
// Editor picks a runtime and the editor is empty.
export const defaultCode = {
  python313: py_http_hello,
  python314: py_http_hello,
  node22:    node_http_hello,
  node24:    node_http_hello,
}

// Categories in display order — used by the picker UX to group entries.
export const categoryOrder = ['Starter', 'Webhooks', 'Auth', 'Utility', 'Scheduled', 'Showcase']
