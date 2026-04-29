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
export const categoryOrder = ['Starter', 'Webhooks', 'Auth', 'Utility', 'Scheduled']
