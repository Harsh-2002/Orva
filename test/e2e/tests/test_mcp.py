#!/usr/bin/env python3
"""The MCP endpoint (`POST /mcp`) — transport contract + per-principal catalog.

Everything here was Go-only until now: nothing outside `backend/internal/mcp`
would have caught a protocol regression, and the endpoint is the one surface
third-party agents (Claude Code, claude.ai connectors, ChatGPT) speak to.

Four properties are asserted:

  1. **The 2026-07-28 protocol is actually reachable.** It only works on a
     stateless server; with the SDK's default options Orva answered every
     2026-07-28 client with HTTP 400 while still speaking the older version
     fine. A `tools/list` that carries no handshake at all is the cheapest
     proof the stateless path is live.
  2. **Results are `cacheScope: private`.** The SDK stamps every cacheable
     result "public", which invites any intermediary to serve one principal's
     catalog to another. See (4) for why that is a disclosure and not a
     preference.
  3. **Back-compat.** A legacy `initialize` must still negotiate 2025-11-25,
     and — because the server is now stateless — must issue no session id.
  4. **The catalog is per-principal.** An operator key sees all 73
     management tools; a channel token sees exactly the functions bundled
     into its channel, invoke-only, and is refused outright at `/api/v1/*`.
     Two callers hitting the identical URL are entitled to different answers,
     which is precisely what makes (2) load-bearing rather than cosmetic.
"""
import json
import sys
import urllib.error
import urllib.request

from harness import OrvaClient, check, section, summary

# Protocol versions. NEW_PROTOCOL is the one the vendored go-sdk reports as
# latest; LEGACY_PROTOCOL is the previous release, which must keep working.
NEW_PROTOCOL = "2026-07-28"
LEGACY_PROTOCOL = "2025-11-25"

# The new protocol moved the handshake into per-request `_meta` keys.
META_VERSION = "io.modelcontextprotocol/protocolVersion"
META_CAPS = "io.modelcontextprotocol/clientCapabilities"
META_CLIENT = "io.modelcontextprotocol/clientInfo"
META_SERVER = "io.modelcontextprotocol/serverInfo"

FN = "e2e-mcp-fn"
CHAN = "e2e-mcp-channel"
# What sanitiseChannelToolName() must make of FN: lowercase, non-[a-z0-9_]
# collapsed to '_'.
CHAN_TOOL = "e2e_mcp_fn"

# A representative slice of the operator catalog. Asserting on names (rather
# than only a count) is what makes the channel-isolation check meaningful:
# these are the tools a channel token must never see.
OPERATOR_TOOLS = ("list_functions", "create_function", "delete_function",
                  "set_secret", "create_api_key", "get_orva_docs")

_OMIT = object()  # sentinel: "send no _meta at all"


def mcp_call(c, method, params=None, token=_OMIT, protocol=NEW_PROTOCOL,
             meta=_OMIT, wire_version=_OMIT, req_id=1):
    """One JSON-RPC call to /mcp. Returns (http_status, headers, message).

    OrvaClient cannot be reused for this: it authenticates with X-Orva-API-Key
    against /api/v1, while /mcp is a bearer surface that also needs the
    transport's Mcp-Protocol-Version / Mcp-Method wire headers — and answers a
    plain POST with an SSE frame rather than a JSON body.

    `token=None` sends no credential (the 401 cases); `meta`/`wire_version`
    default to a well-formed 2026-07-28 handshake and can be overridden to
    exercise the protocol's rejection paths.
    """
    tok = c.key if token is _OMIT else token
    if meta is _OMIT:
        meta = {META_VERSION: protocol, META_CAPS: {},
                META_CLIENT: {"name": "orva-e2e", "version": "1"}} if protocol else None
    body = {"jsonrpc": "2.0", "id": req_id, "method": method}
    p = dict(params or {})
    if meta is not None:
        p["_meta"] = meta
    body["params"] = p

    headers = {
        "Content-Type": "application/json",
        # The transport may answer with either framing; advertise both.
        "Accept": "application/json, text/event-stream",
        "Mcp-Method": method,
    }
    wire = protocol if wire_version is _OMIT else wire_version
    if wire:
        headers["Mcp-Protocol-Version"] = wire
    if tok:
        headers["Authorization"] = "Bearer " + tok
    return _raw(c, "POST", "/mcp", headers, json.dumps(body).encode())


def _raw(c, method, path, headers, data=None, timeout=60):
    req = urllib.request.Request(c.base + path, data=data, method=method,
                                 headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw, code, hdrs = resp.read(), resp.status, dict(resp.headers)
    except urllib.error.HTTPError as e:
        raw, code, hdrs = e.read(), e.code, dict(e.headers or {})
    except urllib.error.URLError as e:
        return 0, {}, {"transport_error": str(e)}
    return code, hdrs, _parse_message(raw.decode(errors="replace"))


def _parse_message(text):
    """Decode a JSON-RPC message from either a raw JSON body or SSE framing.

    Streamable HTTP is free to answer a POST with `event: message` /
    `data: {...}` lines instead of a JSON body, and Orva's does exactly that
    for most methods — so a test that only handled one framing would be
    asserting on the transport's mood.
    """
    text = text.strip()
    if not text:
        return {}
    if text.startswith("{"):
        try:
            return json.loads(text)
        except Exception:
            return {"unparsed": text[:400]}
    for line in text.splitlines():
        if not line.startswith("data:"):
            continue
        try:
            msg = json.loads(line[5:].strip())
        except Exception:
            continue
        if isinstance(msg, dict) and "jsonrpc" in msg:
            return msg
    return {"unparsed": text[:400]}


def tool_names(msg):
    result = (msg or {}).get("result") or {}
    return sorted(t.get("name") for t in (result.get("tools") or []) if isinstance(t, dict))


def header(hdrs, name):
    """Case-insensitive header lookup (http.client normalises inconsistently)."""
    for k, v in (hdrs or {}).items():
        if k.lower() == name.lower():
            return v
    return None


def cleanup(c):
    """Silent, best-effort teardown — the RESULT trailer must stay last."""
    try:
        for ch in ((c.get("/api/v1/channels") or {}).get("channels") or []):
            if ch.get("name") == CHAN:
                c.req("DELETE", f"/api/v1/channels/{ch['id']}", expect=range(200, 599))
    except Exception:
        pass
    try:
        for f in ((c.get("/api/v1/functions?limit=10000") or {}).get("functions") or []):
            if f.get("name") == FN:
                c.req("DELETE", f"/api/v1/functions/{f['id']}", expect=range(200, 599))
    except Exception:
        pass


def main():
    c = OrvaClient()
    if not c.key:
        print("ORVA_API_KEY not set", file=sys.stderr)
        return 2
    cleanup(c)
    operator_names = []
    try:
        section("2026-07-28: tools/list with no handshake")
        # No initialize, no session id — the whole point of stateless mode.
        code, hdrs, msg = mcp_call(c, "tools/list")
        check("tools/list -> 200", code == 200, f"status {code}: {str(msg)[:200]}")
        check("no JSON-RPC error", "error" not in (msg or {}), str(msg.get("error"))[:200])
        operator_names = tool_names(msg)
        check("operator catalog is non-empty", len(operator_names) > 0,
              f"{len(operator_names)} tools")
        check("operator catalog is the full management surface",
              len(operator_names) >= 50, f"{len(operator_names)} tools")
        missing = [t for t in OPERATOR_TOOLS if t not in operator_names]
        check("well-known operator tools present", not missing, f"missing={missing}")
        check("tools carry an input schema",
              all(isinstance(t.get("inputSchema"), dict)
                  for t in (msg.get("result") or {}).get("tools") or []),
              str(((msg.get("result") or {}).get("tools") or [{}])[0])[:200])
        check("stateless: no Mcp-Session-Id issued",
              header(hdrs, "Mcp-Session-Id") is None,
              str(header(hdrs, "Mcp-Session-Id")))

        section("cacheScope is private, not public")
        # The SDK defaults every cacheable result to "public", which would let
        # an intermediary hand one principal's catalog to another.
        result = (msg or {}).get("result") or {}
        check("tools/list cacheScope == private", result.get("cacheScope") == "private",
              f"cacheScope={result.get('cacheScope')!r}")
        # 0 == immediately stale: the catalog changes on any deploy/permission
        # change and statelessness removed the channel a list-changed
        # notification would have used, so any positive TTL would be a lie.
        check("tools/list ttlMs == 0", result.get("ttlMs") == 0,
              f"ttlMs={result.get('ttlMs')!r}")

        section("server/discover")
        dcode, _, dmsg = mcp_call(c, "server/discover")
        check("server/discover -> 200", dcode == 200, f"status {dcode}: {str(dmsg)[:200]}")
        dres = (dmsg or {}).get("result") or {}
        versions = dres.get("supportedVersions") or []
        check("advertises 2026-07-28", NEW_PROTOCOL in versions, str(versions)[:200])
        check("still advertises the legacy version", LEGACY_PROTOCOL in versions,
              str(versions)[:200])
        check("newest version is advertised first", versions[:1] == [NEW_PROTOCOL],
              str(versions)[:200])
        caps = dres.get("capabilities") or {}
        check("advertises tools capability", isinstance(caps.get("tools"), dict),
              str(caps)[:200])
        srv = (dres.get("_meta") or {}).get(META_SERVER) or {}
        check("serverInfo identifies orva", srv.get("name") == "orva", str(srv)[:200])
        check("discover carries instructions", bool(dres.get("instructions")),
              str(sorted(dres.keys()))[:200])
        check("discover is cacheScope private", dres.get("cacheScope") == "private",
              f"cacheScope={dres.get('cacheScope')!r}")

        section("back-compat: legacy initialize")
        # Old clients open a session-style handshake; a stateless server must
        # still negotiate the older version for them, just without a session.
        lcode, lhdrs, lmsg = mcp_call(
            c, "initialize",
            params={"protocolVersion": LEGACY_PROTOCOL, "capabilities": {},
                    "clientInfo": {"name": "orva-e2e-legacy", "version": "1"}},
            protocol=None, meta=None, wire_version=None)
        check("legacy initialize -> 200", lcode == 200, f"status {lcode}: {str(lmsg)[:200]}")
        lres = (lmsg or {}).get("result") or {}
        check("negotiates 2025-11-25", lres.get("protocolVersion") == LEGACY_PROTOCOL,
              f"protocolVersion={lres.get('protocolVersion')!r}")
        check("legacy handshake returns capabilities",
              isinstance(lres.get("capabilities"), dict), str(lres)[:200])
        check("legacy handshake issues no session id",
              header(lhdrs, "Mcp-Session-Id") is None,
              str(header(lhdrs, "Mcp-Session-Id")))

        section("transport shape (stateless)")
        auth = {"Authorization": "Bearer " + c.key,
                "Accept": "application/json, text/event-stream"}
        gcode, _, _ = _raw(c, "GET", "/mcp", auth)
        check("GET /mcp -> 405", gcode == 405, f"status {gcode}")
        dlcode, _, _ = _raw(c, "DELETE", "/mcp", auth)
        check("DELETE /mcp -> 405", dlcode == 405, f"status {dlcode}")

        section("_meta contract of the new protocol")
        # clientCapabilities is REQUIRED once protocolVersion says 2026-07-28.
        _, _, bad = mcp_call(c, "tools/list", meta={META_VERSION: NEW_PROTOCOL})
        err = (bad or {}).get("error") or {}
        check("missing clientCapabilities -> -32602", err.get("code") == -32602,
              str(bad)[:240])
        check("the rejection names the missing field",
              "clientCapabilities" in str(err.get("message", "")), str(err)[:240])
        # The wire header and the _meta version must agree: a body claiming the
        # new protocol without the header is refused rather than silently
        # downgraded to the session path.
        _, _, nohdr = mcp_call(c, "tools/list", wire_version=None)
        check("protocolVersion in _meta requires the wire header",
              bool((nohdr or {}).get("error")), str(nohdr)[:240])

        section("auth gate")
        ncode, _, _ = mcp_call(c, "tools/list", token=None)
        check("no bearer -> 401", ncode == 401, f"status {ncode}")
        bcode, _, _ = mcp_call(c, "tools/list", token="orva_not_a_real_key")
        check("bogus bearer -> 401", bcode == 401, f"status {bcode}")

        section("setup: a function bundled into a channel")
        fbody = {"name": FN, "description": "mcp channel member", "runtime": "node",
                 "entrypoint": "handler.js", "timeout_ms": 30000, "memory_mb": 128,
                 "cpus": 1, "network_mode": "none", "auth_mode": "none"}
        fc, fn = c.req("POST", "/api/v1/functions", fbody, expect=range(200, 599))
        check("function create -> 2xx", 200 <= fc < 300, f"status {fc}: {str(fn)[:200]}")
        fid = (fn or {}).get("id") if isinstance(fn, dict) else None
        check("function has id", bool(fid))
        cc, chan = c.req("POST", "/api/v1/channels",
                         {"name": CHAN, "description": "mcp e2e bundle",
                          "function_ids": [fid] if fid else []},
                         expect=range(200, 599))
        check("channel create -> 201", cc == 201, f"status {cc}: {str(chan)[:200]}")
        chan = chan if isinstance(chan, dict) else {}
        chan_id = chan.get("id")
        chan_tok = chan.get("token") or ""
        check("channel token is orva_chn_<32hex>",
              chan_tok.startswith("orva_chn_") and len(chan_tok) == len("orva_chn_") + 32,
              f"token={chan_tok[:16]!r} len={len(chan_tok)}")

        section("channel token sees ONLY its channel (new protocol)")
        # The cross-tenant property. Same URL, same method, same protocol —
        # different principal, different catalog. This is why the results above
        # must never be cached as "public".
        ccode, chdrs, cmsg = mcp_call(c, "tools/list", token=chan_tok)
        check("channel tools/list -> 200", ccode == 200, f"status {ccode}: {str(cmsg)[:200]}")
        cnames = tool_names(cmsg)
        check("channel sees exactly one tool per bundled function",
              cnames == [CHAN_TOOL], f"tools={cnames}")
        # Every no-leak assertion below is guarded on BOTH catalogs being
        # non-empty. Set-difference checks are trivially true on empty inputs
        # ([] & [] is no leak; [] == [] is no change), so without the guards a
        # regression that broke tools/list outright would satisfy them all and
        # this section would report a clean cross-tenant boundary while
        # measuring nothing. Verified: under a forced transport failure these
        # four checks passed while the real catalog was empty.
        both_listed = len(cnames) > 0 and len(operator_names) > 0
        leaked = sorted(set(cnames) & set(operator_names))
        check("channel sees NO operator tool", both_listed and not leaked,
              f"leaked={leaked} channel={len(cnames)} operator={len(operator_names)}")
        check("no operator tool name is reachable at all",
              len(cnames) > 0 and not any(t in cnames for t in OPERATOR_TOOLS),
              f"tools={cnames}")
        check("operator catalog does not contain the channel tool",
              len(operator_names) > 0 and CHAN_TOOL not in operator_names,
              f"operator={len(operator_names)} tools")
        check("channel catalog is a strict subset of the operator catalog size",
              len(cnames) < len(operator_names), f"{len(cnames)} vs {len(operator_names)}")
        cres = (cmsg or {}).get("result") or {}
        check("channel tools/list is cacheScope private",
              cres.get("cacheScope") == "private", f"cacheScope={cres.get('cacheScope')!r}")
        check("channel response issues no session id",
              header(chdrs, "Mcp-Session-Id") is None)
        ctitle = ((((cmsg or {}).get("result") or {}).get("_meta") or {})
                  .get(META_SERVER) or {})
        # Channel mode names the server after the channel, so a downstream
        # agent can tell which bundle it is talking to.
        check("channel serverInfo is scoped to the channel",
              CHAN in str(ctitle.get("title", "")), str(ctitle)[:200])

        section("channel token has no Orva-management authority")
        chan_client = OrvaClient(api_key=chan_tok)
        fn_status = chan_client.status("GET", "/api/v1/functions")
        check("channel token at /api/v1/functions -> 401",
              fn_status == 401, f"status {fn_status}")
        check("channel token at /api/v1/channels -> 401",
              chan_client.status("GET", "/api/v1/channels") == 401)
        rest = {"Authorization": "Bearer " + chan_tok, "Accept": "application/json"}
        rcode, _, _ = _raw(c, "GET", "/api/v1/functions", rest)
        check("channel token as REST bearer -> 401", rcode == 401, f"status {rcode}")

        section("operator key still sees the full catalog")
        # Re-listed after the channel existed: per-request server construction
        # must not have leaked channel mode into the operator path.
        ocode, _, omsg = mcp_call(c, "tools/list")
        onames = tool_names(omsg)
        check("operator tools/list still -> 200", ocode == 200, f"status {ocode}")
        check("operator catalog unchanged by the channel",
              len(onames) > 0 and onames == operator_names,
              f"n={len(onames)} delta={sorted(set(onames) ^ set(operator_names))}")

        section("deleting the channel revokes its token")
        if chan_id:
            dc, _ = c.req("DELETE", f"/api/v1/channels/{chan_id}", expect=range(200, 599))
            check("channel delete -> 2xx", dc in (200, 204), f"status {dc}")
            rcode2, _, _ = mcp_call(c, "tools/list", token=chan_tok)
            check("revoked channel token -> 401", rcode2 == 401, f"status {rcode2}")

        section("cleanup leaves nothing behind")
        if fid:
            dfc, _ = c.req("DELETE", f"/api/v1/functions/{fid}", expect=range(200, 599))
            check("function delete -> 2xx", dfc in (200, 204), f"status {dfc}")
        chans = (c.get("/api/v1/channels") or {}).get("channels") or []
        check("no e2e channel remains", all(x.get("name") != CHAN for x in chans),
              str([x.get("name") for x in chans])[:200])
        fns = (c.get("/api/v1/functions?limit=10000") or {}).get("functions") or []
        check("no e2e function remains", all(x.get("name") != FN for x in fns))
    finally:
        cleanup(c)
    return summary()


if __name__ == "__main__":
    sys.exit(main())
