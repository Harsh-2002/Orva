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
  originalStdoutWrite(hdr);
  originalStdoutWrite(body);
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

    let out;
    try {
      const result = await dispatch(event);
      out = await normaliseResponse(result);
    } catch (err) {
      process.stderr.write(`Handler error: ${err.stack || err.message}\n`);
      out = {
        statusCode: 500,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ error: 'Internal function error', message: err.message }),
      };
    }
    writeFrame({ type: 'response', statusCode: out.statusCode, headers: out.headers, body: out.body });

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
