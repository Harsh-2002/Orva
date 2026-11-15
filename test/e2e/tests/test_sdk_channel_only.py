#!/usr/bin/env python3
"""Channel-only SDK authority, removed HTTP surfaces, and private egress refusal.

Only owned functions/jobs are removed. The matching shipped private-network rule
is snapshot/restored. External runners must explicitly supply a safe numeric
ORVA_E2E_CONTROL_PLANE_URL; missing network coverage is never reported as PASS.
"""
import datetime
import ipaddress
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from harness import OrvaClient, check, section, skip, summary


PRIVATE = tuple(ipaddress.ip_network(n) for n in
                ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16'))
RESERVED = ['ORVA_SDK_FD', 'ORVA_SDK_PROTO']
FAKE_TOKEN = 'channel-only-test-not-a-credential'

SOURCES = {
    'node': r"""
let httpAttempts = 0;
const noHTTP = () => { httpAttempts++; throw new Error('HTTP transport attempted'); };
for (const name of ['http', 'https']) {
  const mod = require(name); mod.request = noHTTP; mod.get = noHTTP;
}
globalThis.fetch = noHTTP;
const initialEnv = Object.keys(process.env).filter(k =>
  ['ORVA_SDK_FD', 'ORVA_SDK_PROTO', 'ORVA_INTERNAL_TOKEN', 'ORVA_API_BASE'].includes(k.toUpperCase())).sort();
const fd = process.env.ORVA_SDK_FD, proto = process.env.ORVA_SDK_PROTO;
if (process.env.CHANNEL_ONLY_BREAK === '1') {
  delete process.env.ORVA_SDK_FD; delete process.env.ORVA_SDK_PROTO;
  process.env.ORVA_INTERNAL_TOKEN = 'channel-only-test-not-a-credential';
  process.env.ORVA_API_BASE = 'http://127.0.0.1:1';
}
const orva = require('orva');
const net = require('net');
const ok = body => ({statusCode: 200, body: JSON.stringify(body)});
exports.handler = async event => {
  const q = JSON.parse(event.body || '{}');
  const out = {function_id: process.env.ORVA_FUNCTION_ID, initial_env: initialEnv, fd, proto};
  if (q.op === 'echo') return ok({...out, value: q.value});
  if (q.op === 'probe') {
    const result = await new Promise(resolve => {
      const socket = new net.Socket();
      const finish = data => { socket.destroy(); resolve(data); };
      socket.setTimeout(3000);
      socket.once('connect', () => finish({connected: true}));
      socket.once('error', e => finish({connected: false, code: e.code}));
      socket.once('timeout', () => finish({connected: false, code: 'ETIMEDOUT'}));
      socket.connect(q.port, q.host);
    });
    return ok({...out, ...result});
  }
  if (q.op === 'unavailable') {
    out.errors = [];
    for (const call of [() => orva.kv.put('must-not-write', q.value),
                        () => orva.invoke(q.target, {op: 'effect', value: q.value})]) {
      try { await call(); out.errors.push({unexpected_success: true}); }
      catch (e) { out.errors.push({name: e.name, message: e.message}); }
    }
  } else if (q.op === 'effect') {
    await orva.kv.put('legacy-effect', q.value);
  } else {
    await orva.kv.put('channel-value', q.value);
    out.value = await orva.kv.get('channel-value');
    out.result = await orva.invoke(q.target, {op: 'echo', value: q.value});
  }
  return ok({...out, http_attempts: httpAttempts});
};
""",
    'python': r"""
import errno
import http.client
import json
import os
import socket
import urllib.request

http_attempts = 0
def no_http(*args, **kwargs):
    global http_attempts
    http_attempts += 1
    raise RuntimeError('HTTP transport attempted')

urllib.request.urlopen = no_http
http.client.HTTPConnection.connect = no_http
http.client.HTTPSConnection.connect = no_http
initial_env = sorted(k for k in os.environ if k.upper() in {
    'ORVA_SDK_FD', 'ORVA_SDK_PROTO', 'ORVA_INTERNAL_TOKEN', 'ORVA_API_BASE'})
fd, proto = os.environ.get('ORVA_SDK_FD'), os.environ.get('ORVA_SDK_PROTO')
if os.environ.get('CHANNEL_ONLY_BREAK') == '1':
    os.environ.pop('ORVA_SDK_FD', None)
    os.environ.pop('ORVA_SDK_PROTO', None)
    os.environ['ORVA_INTERNAL_TOKEN'] = 'channel-only-test-not-a-credential'
    os.environ['ORVA_API_BASE'] = 'http://127.0.0.1:1'
import orva

def handler(event):
    q = json.loads(event.get('body') or '{}')
    out = {'function_id': os.environ.get('ORVA_FUNCTION_ID'),
           'initial_env': initial_env, 'fd': fd, 'proto': proto}
    if q.get('op') == 'echo':
        return {'statusCode': 200, 'body': json.dumps({**out, 'value': q['value']})}
    if q.get('op') == 'probe':
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.settimeout(3)
            try:
                sock.connect((q['host'], q['port']))
                out['connected'] = True
            except OSError as exc:
                out.update(connected=False, code=errno.errorcode.get(exc.errno, 'ETIMEDOUT'))
    elif q.get('op') == 'unavailable':
        out['errors'] = []
        for call in (lambda: orva.kv.put('must-not-write', q['value']),
                     lambda: orva.invoke(q['target'], {'op': 'effect', 'value': q['value']})):
            try:
                call()
                out['errors'].append({'unexpected_success': True})
            except Exception as exc:
                out['errors'].append({'name': type(exc).__name__, 'message': str(exc)})
    elif q.get('op') == 'effect':
        orva.kv.put('legacy-effect', q['value'])
    else:
        orva.kv.put('channel-value', q['value'])
        out['value'] = orva.kv.get('channel-value')
        out['result'] = orva.invoke(q['target'], {'op': 'echo', 'value': q['value']})
    return {'statusCode': 200, 'body': json.dumps({**out, 'http_attempts': http_attempts})}
""",
}


def wait_for(label, probe, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = probe()
        if result:
            return result
        time.sleep(0.2)
    raise AssertionError('timed out waiting for ' + label)


def deploy(c, owned, suffix, runtime, network, broken=False):
    filename = 'handler.js' if runtime == 'node' else 'handler.py'
    fn = c.post('/api/v1/functions', {
        'name': f'e2e-sdk-only-{suffix}-{runtime}-{network}' + ('-broken' if broken else ''),
        'runtime': runtime, 'entrypoint': filename, 'sdk': True,
        'network_mode': network, 'auth_mode': 'none', 'memory_mb': 64,
        'cpus': 0.1, 'max_concurrency': 1, 'timeout_ms': 15000,
        'env_vars': {'CHANNEL_ONLY_BREAK': '1'} if broken else {},
    })
    owned.append(fn['id'])
    _, submitted = c.req('POST', f"/api/v1/functions/{fn['id']}/deploy-inline", {
        'code': SOURCES[runtime], 'filename': filename,
    }, expect=(200, 202))

    def built():
        row = c.get('/api/v1/deployments/' + submitted['deployment_id'])
        if row['status'] == 'failed':
            raise AssertionError(fn['name'] + ' deployment failed: ' + str(row))
        return (row['status'] in ('succeeded', 'deployed')
                and c.get(f"/api/v1/functions/{fn['id']}")['status'] == 'active')

    wait_for(fn['name'] + ' specific deployment', built)
    return fn


def call(c, fn, **payload):
    code, result = c.req('POST', '/fn/' + fn['id'], payload, expect=range(100, 600))
    if code != 200 or not isinstance(result, dict):
        raise AssertionError(f"{fn['name']} sandbox response: {code}: {result!r}")
    check(fn['name'] + ' executed its own handler', result.get('function_id') == fn['id'])
    return result


def roundtrip(c, fn, target, value):
    out = call(c, fn, op='channel', target=target['name'], value=value)
    check(fn['name'] + ' has only channel capability, no legacy token/base',
          out.get('initial_env') == RESERVED and out.get('fd') == '3' and out.get('proto') == '1')
    check(fn['name'] + ' SDK never attempts HTTP', out.get('http_attempts') == 0)
    check(fn['name'] + ' exact KV roundtrip', out.get('value') == value)
    result = out.get('result') or {}
    check(fn['name'] + ' exact cross-runtime F2F roundtrip',
          result.get('statusCode') == 200
          and result.get('body', {}).get('function_id') == target['id']
          and result.get('body', {}).get('value') == value)
    check(fn['name'] + ' operator observes persisted channel KV',
          c.get(f"/api/v1/functions/{fn['id']}/kv/channel-value").get('value') == value)


def raw(c, method, path, body=None, admin=False):
    headers = {'Content-Type': 'application/json', 'X-Orva-Internal-Token': FAKE_TOKEN}
    if admin:
        headers['X-Orva-API-Key'] = c.key
    request = urllib.request.Request(c.base + path, method=method, headers=headers,
                                     data=json.dumps(body).encode() if body is not None else None)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return response.status
    except urllib.error.HTTPError as exc:
        return exc.code


def removed_http(c, fn, suffix):
    fid = fn['id']
    key = f'/api/v1/_kv/{fid}/legacy-effect'
    routes = [
        ('GET', f'/api/v1/_kv/{fid}', None), ('GET', key, None),
        ('PUT', key, {'value': 'forbidden'}), ('DELETE', key, None),
        ('POST', f'/api/v1/_kv/{fid}/batch', {'ops': [{'op': 'put', 'key': 'legacy-effect', 'value': 1}]}),
        ('POST', key + '/incr', {'delta': 1}),
        ('POST', key + '/cas', {'expected': None, 'value': 1}),
        ('POST', f"/api/v1/_internal/invoke/{fn['name']}", {'op': 'effect', 'value': 1}),
        ('POST', f"/api/v1/_internal/invoke/{fn['name']}/stream", {'op': 'effect', 'value': 1}),
        ('POST', '/api/v1/_internal/spans', {'name': suffix}),
        ('POST', '/api/v1/_internal/crons', {'name': suffix, 'schedule': '0 3 * * *', 'enabled': False}),
    ]
    for admin in (True, False):
        for method, path, body in routes:
            code = raw(c, method, path, body, admin=admin)
            check(f'{method} {path} ' + ('operator' if admin else 'fake SDK token') + ' never reaches core',
                  code == (404 if admin else 401), f'status={code}')
    check('removed HTTP routes did not write KV or invoke target',
          c.status('GET', f'/api/v1/functions/{fid}/kv/legacy-effect') == 404)
    before = c.get(f'/api/v1/jobs?function_id={fid}')['jobs']
    body = {'function_id': fid, 'payload': {'op': 'effect', 'value': suffix},
            'scheduled_at': (datetime.datetime.now(datetime.timezone.utc)
                             + datetime.timedelta(hours=1)).isoformat(), 'max_attempts': 1}
    check('public jobs rejects former SDK token header without operator authority',
          raw(c, 'POST', '/api/v1/jobs', body) == 401)
    check('rejected token-only POST jobs creates no queue row',
          c.get(f'/api/v1/jobs?function_id={fid}')['jobs'] == before == [])
    # A stale SDK header must not poison a valid operator request either.
    check('operator key still enqueues public jobs even with a fake legacy header',
          raw(c, 'POST', '/api/v1/jobs', body, admin=True) == 201)
    jobs = c.get(f'/api/v1/jobs?function_id={fid}')['jobs']
    check('exactly one operator job exists', len(jobs) == 1 and jobs[0].get('function_id') == fid)
    for job in jobs:
        c.delete('/api/v1/jobs/' + job['id'])
    check('rejected HTTP job did not produce a KV side effect',
          c.status('GET', f'/api/v1/functions/{fid}/kv/legacy-effect') == 404)


def control_plane(value):
    parsed = urllib.parse.urlsplit(value)
    address = ipaddress.IPv4Address(parsed.hostname or '')
    if (parsed.scheme != 'http' or parsed.username or parsed.password
            or parsed.path not in ('', '/') or parsed.query or parsed.fragment
            or not parsed.port or not any(address in network for network in PRIVATE)):
        raise ValueError('control-plane URL must be an explicit private numeric http://IPv4:port origin')
    return value.rstrip('/'), address, parsed.port


def network_case(c, functions, url, suffix, saved_rules):
    base, address, port = control_plane(url)
    # No operator credential goes to an unverified URL. A unique owned public
    # fixture proves both endpoint identity and liveness before any block claim.
    fn = functions['node', 'egress']
    request = urllib.request.Request(base + '/fn/' + fn['id'], method='POST',
        data=json.dumps({'op': 'echo', 'value': suffix}).encode(),
        headers={'Content-Type': 'application/json'})
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=15) as response:
        identity = json.load(response)
    if identity.get('function_id') != fn['id'] or identity.get('value') != suffix:
        raise AssertionError('control-plane URL did not reach this disposable instance')
    check('numeric private endpoint is live and belongs to this exact test instance', True)

    rules = c.get('/api/v1/firewall/rules')['rules']
    rule = next((r for r in rules if r.get('kind') == 'suggested'
                 and r.get('value') in {str(n) for n in PRIVATE}
                 and address in ipaddress.ip_network(r['value'])), None)
    if not rule:
        raise AssertionError('matching shipped private-network suggestion is missing')
    blocking = any(r.get('enabled') and r.get('rule_type') == 'cidr'
                   and address in ipaddress.ip_network(r['value']) for r in rules)
    if not blocking:
        for runtime in ('node', 'python'):
            out = call(c, functions[runtime, 'egress'], op='probe', host=str(address), port=port)
            check(runtime + ' numeric control-plane TCP reaches listener before private block',
                  out.get('connected') is True, str(out))

    if not rule['enabled']:
        saved_rules[rule['id']] = rule['enabled']
        c.put('/api/v1/firewall/rules/' + str(rule['id']), {'enabled': True})
    resolved = c.post('/api/v1/firewall/resolve', {})['status']
    check('private block compiles into an enforced, current policy',
          resolved.get('enforced') is True and resolved.get('policy_stale') is False
          and resolved.get('policy_rule_counts', {}).get('reject', 0) > 0)
    cp = resolved.get('control_plane_allow')
    check('policy exposes no automatic control-plane exemption',
          cp is None or (isinstance(cp, dict) and not cp.get('addrs') and not cp.get('port')))
    enabled_rules = c.get('/api/v1/firewall/rules')['rules']
    check('matching default private-network suggestion is enabled for the refusal test',
          any(r['id'] == rule['id'] and r['enabled'] for r in enabled_rules))
    for runtime in ('node', 'python'):
        fn = functions[runtime, 'egress']
        c.put('/api/v1/functions/' + fn['id'], {'memory_mb': 64})
        out = call(c, fn, op='probe', host=str(address), port=port)
        check(runtime + ' egress refuses live numeric private Orva address via policy, not DNS/timeout',
              out.get('connected') is False and out.get('code') == 'ECONNREFUSED', str(out))
        roundtrip(c, fn, functions['python' if runtime == 'node' else 'node', 'none'],
                  {'after_private_block': suffix, 'runtime': runtime})


def main():
    c = OrvaClient()
    if not c.key:
        print('ORVA_API_KEY not set', file=sys.stderr)
        return 2
    owned, functions, saved_rules = [], {}, {}
    suffix = uuid.uuid4().hex[:12]
    url = os.environ.get('ORVA_E2E_CONTROL_PLANE_URL', '')
    try:
        section('Node/Python SDK enabled in none and egress: channel only')
        for runtime in ('node', 'python'):
            for network in ('none', 'egress'):
                functions[runtime, network] = deploy(c, owned, suffix, runtime, network)
        for (runtime, network), fn in functions.items():
            target = functions['python' if runtime == 'node' else 'node', 'none']
            roundtrip(c, fn, target, {'runtime': runtime, 'network': network, 'marker': suffix})

        section('missing channel fails closed even with legacy credentials/base supplied')
        for runtime in ('node', 'python'):
            fn = deploy(c, owned, suffix, runtime, 'egress', broken=True)
            target = functions['node', 'none']
            out = call(c, fn, op='unavailable', target=target['name'], value=suffix)
            check(runtime + ' originally received only channel capability', out.get('initial_env') == RESERVED)
            errors = out.get('errors') or []
            check(runtime + ' KV and F2F fail clearly when channel is removed',
                  len(errors) == 2 and all(e.get('name') == 'OrvaUnavailableError'
                                          and 'sdk' in e.get('message', '').lower() for e in errors), str(errors))
            check(runtime + ' unavailable SDK never attempts HTTP fallback', out.get('http_attempts') == 0)
            check(runtime + ' unavailable SDK has no KV side effects',
                  c.status('GET', f"/api/v1/functions/{fn['id']}/kv/must-not-write") == 404
                  and c.status('GET', f"/api/v1/functions/{target['id']}/kv/legacy-effect") == 404)

        section('removed legacy routes and public jobs authority')
        removed_http(c, functions['node', 'none'], suffix)
        section('private numeric Orva HTTP blocked while SDK channel still works')
        if url:
            network_case(c, functions, url, suffix, saved_rules)
        else:
            print('NETWORK COVERAGE UNAVAILABLE: supply --control-plane-url or '
                  'ORVA_E2E_CONTROL_PLANE_URL for this disposable instance; not a network pass.')
    except Exception as exc:
        check('channel-only SDK journey', False, str(exc))
    finally:
        for rule_id, enabled in saved_rules.items():
            try:
                c.put('/api/v1/firewall/rules/' + str(rule_id), {'enabled': enabled})
                check('private-network rule restored', any(
                    r['id'] == rule_id and r['enabled'] is enabled
                    for r in c.get('/api/v1/firewall/rules')['rules']))
            except Exception as exc:
                check('restore private-network rule', False, str(exc))
        if owned:
            try:
                def drained():
                    active = c.get('/api/v1/system/metrics.json')['active_requests']
                    writer = c.get('/api/v1/system/health')['writer']
                    return active == 0 and all(writer[k] == 0 for k in (
                        'critical_queue_bytes', 'activity_queue_bytes', 'telemetry_queue_bytes'))
                wait_for('accepted writes to drain', drained, timeout=30)
            except Exception as exc:
                check('writer drain before cleanup', False, str(exc))
        for fid in reversed(owned):
            try:
                for job in c.get(f'/api/v1/jobs?function_id={fid}')['jobs']:
                    c.delete('/api/v1/jobs/' + job['id'])
                c.req('DELETE', '/api/v1/functions/' + fid, expect=(200, 204, 404))
            except Exception as exc:
                check('owned fixture cleanup', False, str(exc))
    result = summary()
    if not url and result == 0:
        return skip('functional channel checks passed; private egress coverage unavailable: '
                    'explicit safe ORVA_E2E_CONTROL_PLANE_URL required')
    return result


if __name__ == '__main__':
    sys.exit(main())
