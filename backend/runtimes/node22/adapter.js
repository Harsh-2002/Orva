// Orva Node.js adapter — universal handler loader.
//
// Accepts a wide range of export conventions so existing code from AWS
// Lambda, Cloudflare Workers, Vercel, Next.js, Netlify, and generic
// Node/Express style deploys runs with zero changes:
//
//   AWS Lambda         : exports.handler = async (event, context) => ...
//                        exports.lambda_handler = async (event, context) => ...
//   Cloudflare Worker  : export default { fetch(request, env, ctx) { ... } }
//                        addEventListener('fetch', e => e.respondWith(...))
//   Vercel / Next API  : export default async function handler(req, res) { ... }
//   Netlify / generic  : exports.handler = async (event) => ...
//   Plain function     : module.exports = async (event) => ...
//
// The adapter normalises all of them to a { statusCode, headers, body }
// response envelope, the native Orva protocol.

const path = require('path');
const Module = require('module');

const FUNCTION_DIR = '/code';
const entrypoint = process.env.ORVA_ENTRYPOINT || 'handler.js';
const handlerPath = path.join(FUNCTION_DIR, entrypoint);

// Make the bundled `orva` SDK module (kv / invoke / jobs) resolvable
// from user code via `require('orva')`. The package lives at
// /opt/orva/node_modules/orva/; injecting that dir at the front of
// Module._nodeModulePaths ensures user modules can find it without
// the user having to install it. User-installed deps still resolve
// normally via /code/node_modules.
const _origNodeModulePaths = Module._nodeModulePaths;
Module._nodeModulePaths = function (from) {
  return ['/opt/orva/node_modules', ...(_origNodeModulePaths.call(this, from) || [])];
};

// Preserve stdout for the protocol response; reroute user output to stderr.
const originalStdoutWrite = process.stdout.write.bind(process.stdout);
const writeProtocol = (s) => originalStdoutWrite(s);
process.stdout.write = process.stderr.write.bind(process.stderr);
console.log = (...a) => process.stderr.write(a.map(String).join(' ') + '\n');
console.info = console.log;
console.debug = console.log;
console.warn = console.log;
console.error = console.log;

let mod;
try {
  mod = require(handlerPath);
} catch (err) {
  process.stderr.write(`Failed to load handler from ${handlerPath}: ${err.message}\n`);
  process.exit(1);
}

// Unwrap ESM default export shim (Babel/TS sometimes emit { default: fn }).
if (mod && typeof mod === 'object' && mod.__esModule && mod.default) {
  mod = mod.default;
}

// Resolve a callable from whatever shape the user exported.
let handler = null;
let style = null; // "lambda" | "worker" | "vercel" | "plain"

if (typeof mod === 'function') {
  handler = mod;
  // A plain function could be Lambda-style or Vercel-style — decide by arity.
  style = mod.length >= 2 ? 'vercel-or-lambda' : 'plain';
} else if (mod && typeof mod === 'object') {
  // Cloudflare Worker: { fetch(request, env, ctx) }
  if (typeof mod.fetch === 'function') {
    handler = mod.fetch;
    style = 'worker';
  } else if (typeof mod.handler === 'function') {
    handler = mod.handler;
    style = 'lambda';
  } else if (typeof mod.lambda_handler === 'function') {
    handler = mod.lambda_handler;
    style = 'lambda';
  } else if (typeof mod.main === 'function') {
    handler = mod.main;
    style = 'lambda';
  } else if (typeof mod.default === 'function') {
    handler = mod.default;
    style = 'plain';
  }
}

if (!handler) {
  process.stderr.write(
    `Module at ${handlerPath} does not export a usable handler. ` +
    `Expected one of: handler, lambda_handler, main, fetch, default, ` +
    `or a default function export.\n`
  );
  process.exit(1);
}

// ── Helpers to bridge calling conventions ──────────────────────────────

function buildLambdaContext(event) {
  const hdrs = (event && event.headers) || {};
  return {
    functionName: process.env.ORVA_FUNCTION_NAME || '',
    awsRequestId: hdrs['x-orva-execution-id'] || '',
    invokedFunctionArn: '',
    memoryLimitInMB: process.env.ORVA_MEMORY_MB || '',
    logGroupName: 'orva',
    logStreamName: hdrs['x-orva-execution-id'] || '',
    getRemainingTimeInMillis: () => Number(process.env.ORVA_TIMEOUT_MS || 30000),
  };
}

// Minimal Request/Response polyfills for Cloudflare Worker style. If the
// runtime provides them natively (Node 18+ has global fetch), prefer those.
function buildWorkerRequest(event) {
  const url = `http://localhost${event.path || '/'}`;
  if (typeof Request === 'function') {
    try {
      return new Request(url, {
        method: event.method || 'GET',
        headers: event.headers || {},
        body: ['GET', 'HEAD'].includes((event.method || 'GET').toUpperCase())
          ? undefined
          : (event.body || ''),
      });
    } catch { /* fall through to shim */ }
  }
  return {
    method: event.method || 'GET',
    url,
    headers: new Map(Object.entries(event.headers || {})),
    body: event.body || '',
    async text() { return this.body; },
    async json() { return JSON.parse(this.body || '{}'); },
    async arrayBuffer() { return Buffer.from(this.body || '').buffer; },
  };
}

async function normaliseResponse(ret) {
  // Already in Orva envelope.
  if (ret && typeof ret === 'object' && 'statusCode' in ret) {
    return {
      statusCode: ret.statusCode || 200,
      headers: ret.headers || { 'Content-Type': 'application/json' },
      body: typeof ret.body === 'string' ? ret.body : JSON.stringify(ret.body || {}),
    };
  }
  // Fetch API Response.
  if (ret && typeof ret === 'object' && typeof ret.status === 'number' && typeof ret.text === 'function') {
    const body = await ret.text();
    const headers = {};
    if (ret.headers && typeof ret.headers.forEach === 'function') {
      ret.headers.forEach((v, k) => { headers[k] = v; });
    }
    return { statusCode: ret.status, headers, body };
  }
  // Anything else → JSON-encode as the body.
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: typeof ret === 'string' ? ret : JSON.stringify(ret ?? null),
  };
}

// Vercel/Next-style (req, res) detection: wrap to capture the response.
function invokeVercelStyle(fn, event) {
  return new Promise((resolve, reject) => {
    const req = {
      method: event.method || 'GET',
      url: event.path || '/',
      headers: event.headers || {},
      body: (() => {
        try { return JSON.parse(event.body || 'null'); } catch { return event.body; }
      })(),
      query: {},
    };
    let statusCode = 200;
    const headers = {};
    let body = '';
    const res = {
      status(c) { statusCode = c; return this; },
      setHeader(k, v) { headers[k] = v; return this; },
      getHeader(k) { return headers[k]; },
      json(o) { headers['Content-Type'] = 'application/json'; body = JSON.stringify(o); this.end(); },
      send(x) { body = typeof x === 'string' ? x : JSON.stringify(x); this.end(); },
      write(x) { body += typeof x === 'string' ? x : String(x); },
      end(x) {
        if (x !== undefined) body = typeof x === 'string' ? x : String(x);
        resolve({ statusCode, headers, body });
      },
    };
    Promise.resolve()
      .then(() => fn(req, res))
      .catch(reject);
  });
}

// ── Framed stdio protocol ──────────────────────────────────────────────
// Wire format: 4-byte big-endian uint32 length, then N bytes of UTF-8 JSON.
// Applied symmetrically to stdin (proxy → adapter) and stdout (adapter →
// proxy). Length-prefix chosen over JSONL because it is binary-safe and
// handles the full 6 MB MaxBodyBytes without escape gymnastics.

async function dispatch(event) {
  if (style === 'worker') {
    const req = buildWorkerRequest(event);
    const env = { ...process.env };
    const ctx = { waitUntil: () => {}, passThroughOnException: () => {} };
    return await handler(req, env, ctx);
  }
  if (style === 'vercel-or-lambda') {
    // Two-arg function: try Lambda first (returns a value). If it returns
    // undefined OR throws a TypeError trying to treat ctx as `res`, assume
    // Vercel (req, res) style and replay via invokeVercelStyle.
    const ctx = buildLambdaContext(event);
    let lambdaResult;
    let lambdaErr;
    try {
      lambdaResult = await handler(event, ctx);
    } catch (e) {
      lambdaErr = e;
    }
    const looksLikeVercelMiss =
      lambdaErr &&
      (lambdaErr instanceof TypeError) &&
      /is not a function|Cannot read propert/i.test(String(lambdaErr.message));
    if (lambdaResult === undefined || looksLikeVercelMiss) {
      return await invokeVercelStyle(handler, event);
    }
    if (lambdaErr) throw lambdaErr;
    return lambdaResult;
  }
  if (style === 'lambda') return await handler(event, buildLambdaContext(event));
  return await handler(event);
}

function writeFrame(obj) {
  const body = Buffer.from(JSON.stringify(obj), 'utf-8');
  const hdr = Buffer.alloc(4);
  hdr.writeUInt32BE(body.length, 0);
  // Node's process.stdout.write returns false if the kernel pipe buffer
  // is full (backpressure from the proxy). We ignore the return — the
  // next write blocks naturally until drain. EPIPE is rethrown to the
  // caller so streaming loops can stop iterating on client disconnect.
  originalStdoutWrite(hdr);
  originalStdoutWrite(body);
}

// v0.4 C1: streaming helpers — translate user-yielded values into
// `chunk` frames. data may be Buffer | string | Uint8Array | object.
function streamChunk(data) {
  let buf;
  if (data == null) {
    buf = Buffer.alloc(0);
  } else if (Buffer.isBuffer(data)) {
    buf = data;
  } else if (data instanceof Uint8Array) {
    buf = Buffer.from(data.buffer, data.byteOffset, data.byteLength);
  } else if (typeof data === 'string') {
    buf = Buffer.from(data, 'utf-8');
  } else {
    buf = Buffer.from(JSON.stringify(data), 'utf-8');
  }
  writeFrame({ type: 'chunk', data: buf.length === 0 ? '' : buf.toString('base64') });
}

function looksLikeHead(item) {
  if (!item || typeof item !== 'object') return null;
  if (!('statusCode' in item)) return null;
  return {
    status: item.statusCode || 200,
    headers: item.headers || { 'Content-Type': 'text/plain' },
    body: 'body' in item ? item.body : null,
  };
}

// streamIterable consumes any sync/async iterable and emits the
// streaming protocol exchange. When streamingEnabled is false we
// buffer everything into a single response frame for back-compat —
// operators flipping the system_config flag get pre-C1 behaviour
// without redeploying.
async function streamIterable(iterable, streamingEnabled, keepaliveMs) {
  if (!streamingEnabled) {
    let head = null;
    const parts = [];
    const collect = (item) => {
      if (head === null) {
        const detected = looksLikeHead(item);
        if (detected) {
          head = { status: detected.status, headers: detected.headers };
          if (detected.body != null) parts.push(typeof detected.body === 'string' ? detected.body : String(detected.body));
          return;
        }
        head = { status: 200, headers: { 'Content-Type': 'text/plain' } };
      }
      if (Buffer.isBuffer(item)) parts.push(item.toString('utf-8'));
      else if (item instanceof Uint8Array) parts.push(Buffer.from(item).toString('utf-8'));
      else if (typeof item === 'string') parts.push(item);
      else parts.push(String(item));
    };
    for await (const item of iterable) collect(item);
    if (head === null) head = { status: 200, headers: { 'Content-Type': 'text/plain' } };
    writeFrame({
      type: 'response', statusCode: head.status,
      headers: head.headers, body: parts.join(''),
    });
    return;
  }

  let headSent = false;
  let lastEmit = Date.now();
  let hbTimer = null;

  const sendHead = (status, headers) => {
    if (headSent) return;
    writeFrame({ type: 'response_start', statusCode: status, headers });
    headSent = true;
  };

  const startHeartbeat = () => {
    if (hbTimer) return;
    // setInterval fires regardless of how busy the loop is. The check
    // against lastEmit prevents an empty chunk from racing a real one;
    // worst case we emit one extra empty chunk per period, which is
    // harmless on the wire.
    hbTimer = setInterval(() => {
      if (Date.now() - lastEmit >= keepaliveMs) {
        try {
          writeFrame({ type: 'chunk', data: '' });
          lastEmit = Date.now();
        } catch { /* pipe closed — clearInterval below */ }
      }
    }, keepaliveMs);
  };

  try {
    for await (const item of iterable) {
      if (!headSent) {
        const detected = looksLikeHead(item);
        if (detected) {
          sendHead(detected.status, detected.headers);
          startHeartbeat();
          if (detected.body != null && detected.body !== '') {
            streamChunk(detected.body);
            lastEmit = Date.now();
          }
          continue;
        }
        sendHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
        startHeartbeat();
      }
      streamChunk(item);
      lastEmit = Date.now();
    }
    if (!headSent) sendHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
    writeFrame({ type: 'response_end' });
  } catch (err) {
    // EPIPE = client disconnected mid-stream. Stop iterating; the
    // worker continues serving subsequent requests if its stdin is
    // still open. Anything else we re-raise so the outer try/catch
    // emits an error frame BEFORE the head went out.
    if (err && err.code !== 'EPIPE') {
      if (!headSent) throw err;
      // Head already flew — nothing useful to surface. Best-effort end.
      try { writeFrame({ type: 'response_end' }); } catch {}
    }
  } finally {
    if (hbTimer) clearInterval(hbTimer);
  }
}

// drainReadableStream yields chunks from a fetch-API ReadableStream
// (e.g. `new Response(stream).body`). Used when the handler returns a
// Response whose body is a stream — we surface it as if the user had
// written an async generator yielding bytes.
async function* drainReadableStream(readable) {
  const reader = readable.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      if (value) yield value;
    }
  } finally {
    try { reader.releaseLock(); } catch {}
  }
}

// readExactly reads exactly n bytes from process.stdin, resolving with null
// on EOF. Works by concatenating the available readable chunks.
function readExactly(n) {
  return new Promise((resolve, reject) => {
    const out = [];
    let have = 0;
    const stdin = process.stdin;

    const onReadable = () => {
      let chunk;
      while ((chunk = stdin.read(Math.min(n - have, 65536))) !== null) {
        out.push(chunk);
        have += chunk.length;
        if (have >= n) {
          cleanup();
          return resolve(Buffer.concat(out, have));
        }
      }
    };
    const onEnd = () => { cleanup(); resolve(null); };
    const onError = (err) => { cleanup(); reject(err); };
    const cleanup = () => {
      stdin.removeListener('readable', onReadable);
      stdin.removeListener('end', onEnd);
      stdin.removeListener('error', onError);
    };

    stdin.on('readable', onReadable);
    stdin.on('end', onEnd);
    stdin.on('error', onError);
    // In case data is already buffered.
    onReadable();
  });
}

async function readFrame() {
  const header = await readExactly(4);
  if (!header) return null;
  const len = header.readUInt32BE(0);
  if (len === 0) return {};
  const payload = await readExactly(len);
  if (!payload) return null;
  try {
    return JSON.parse(payload.toString('utf-8'));
  } catch (err) {
    return { type: 'request', event: { method: 'POST', path: '/', headers: {}, body: '' } };
  }
}

(async () => {
  // Optional recycle cap: after MAX_REQUESTS dispatches, exit so the pool
  // respawns and we avoid any slow memory creep in user code.
  const maxReqs = Number(process.env.ORVA_MAX_REQUESTS || 0);
  let served = 0;

  for (;;) {
    const frame = await readFrame();
    if (!frame) process.exit(0); // stdin EOF = clean shutdown
    if (frame.type === 'quit') {
      writeFrame({ type: 'bye' });
      process.exit(0);
    }
    if (frame.type !== 'request') continue;

    const event = frame.event || { method: 'POST', path: '/', headers: {}, body: '' };
    // Propagate call depth into env so orva.invoke()'s SDK can forward
    // it on outbound nested calls. Otherwise the host's depth guard
    // never trips on recursion.
    const _hdrs = event.headers || {};
    const _depth = _hdrs['x-orva-call-depth'] || _hdrs['X-Orva-Call-Depth'] || '';
    if (_depth) process.env.ORVA_CALL_DEPTH = _depth;
    else delete process.env.ORVA_CALL_DEPTH;

    // v0.5 trace context. Each event carries the trace_id + span_id of
    // this invocation; the SDK reads them from env when issuing nested
    // F2F calls or job enqueues so causal chains stay linked.
    const _tID = _hdrs['x-orva-trace-id'] || _hdrs['X-Orva-Trace-Id'] || '';
    const _sID = _hdrs['x-orva-span-id']  || _hdrs['X-Orva-Span-Id']  || '';
    if (_tID) process.env.ORVA_TRACE_ID = _tID; else delete process.env.ORVA_TRACE_ID;
    if (_sID) process.env.ORVA_SPAN_ID  = _sID; else delete process.env.ORVA_SPAN_ID;

    // v0.4 C1: streaming flag + heartbeat interval ride on per-request
    // headers so the proxy can flip them at runtime without redeploying
    // the worker. Defaults match the system_config seed values.
    const streamingOn = (_hdrs['x-orva-streaming-enabled'] ?? '1') !== '0';
    const keepaliveS = Math.max(1, Number(_hdrs['x-orva-stream-keepalive-seconds'] ?? '15') || 15);
    const keepaliveMs = keepaliveS * 1000;

    try {
      const result = await dispatch(event);

      // Streaming detection — async iterables, sync iterables, or a
      // Response whose body is a ReadableStream. Order matters:
      // Response objects are also iterable (ReadableStream is async-
      // iterable in Node 18+) so we MUST check the Response branch
      // first to extract status + headers, then drain the body stream.
      if (
        result &&
        typeof result === 'object' &&
        typeof result.status === 'number' &&
        result.body && typeof result.body.getReader === 'function'
      ) {
        const headers = {};
        if (result.headers && typeof result.headers.forEach === 'function') {
          result.headers.forEach((v, k) => { headers[k] = v; });
        }
        // Wrap in a generator that prepends the head, then yields chunks.
        async function* withHead() {
          yield { statusCode: result.status, headers, body: '' };
          for await (const chunk of drainReadableStream(result.body)) yield chunk;
        }
        await streamIterable(withHead(), streamingOn, keepaliveMs);
      } else if (
        result != null &&
        typeof result === 'object' &&
        (typeof result[Symbol.asyncIterator] === 'function' ||
         (typeof result[Symbol.iterator] === 'function' && typeof result !== 'string'))
      ) {
        // Exclude strings, Buffers, and arrays from being treated as
        // streaming iterables — those are perfectly valid response
        // bodies and shouldn't surprise the user.
        const isExcluded =
          typeof result === 'string' ||
          Buffer.isBuffer(result) ||
          Array.isArray(result) ||
          result instanceof Uint8Array;
        if (isExcluded) {
          const out = await normaliseResponse(result);
          writeFrame({ type: 'response', statusCode: out.statusCode, headers: out.headers, body: out.body });
        } else {
          await streamIterable(result, streamingOn, keepaliveMs);
        }
      } else {
        const out = await normaliseResponse(result);
        writeFrame({ type: 'response', statusCode: out.statusCode, headers: out.headers, body: out.body });
      }
    } catch (err) {
      process.stderr.write(`Handler error: ${err.stack || err.message}\n`);
      // If we already started streaming the response head, the proxy is
      // mid-loop and writeFrame on a fresh "response" envelope would
      // confuse it. Best-effort: just close. The proxy will surface the
      // truncation via the connection close.
      try {
        writeFrame({
          type: 'response',
          statusCode: 500,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ error: 'Internal function error', message: err.message }),
        });
      } catch {}
    }

    served++;
    if (maxReqs > 0 && served >= maxReqs) {
      writeFrame({ type: 'bye' });
      process.exit(0);
    }
  }
})().catch((err) => {
  try {
    writeFrame({ type: 'error', fatal: true, message: String(err && err.stack || err) });
  } catch {}
  process.exit(1);
});
