#!/usr/bin/env python3
"""Real-sandbox SDK opt-in, independent of networking, for Node and Python.

No sandbox skips or migration simulation. Uses only this run's owned resources;
network reachability restrictions on the legacy HTTP API belong to PR4.
"""
import sys
import time
import uuid

from harness import OrvaClient, check, section, summary


INJECTED = {
    "ORVA_SDK_FD": "3", "ORVA_SDK_PROTO": "1",
    "ORVA_INTERNAL_TOKEN": "permission-test-not-a-credential",
    "ORVA_API_BASE": "http://127.0.0.1:1",
    "orva_sdk_fd": "3", "Orva_Sdk_Proto": "1",
    "orva_internal_token": "permission-test-not-a-credential",
    "Orva_Api_Base": "http://127.0.0.1:1",
    "PERMISSION_TEST_ENV": "preserved",
}

SOURCES = {
    "node": r"""
const orva = require('orva');
const workerId = require('crypto').randomUUID();
const reserved = ['ORVA_SDK_FD', 'ORVA_SDK_PROTO', 'ORVA_INTERNAL_TOKEN', 'ORVA_API_BASE'];
exports.handler = async event => {
  const q = JSON.parse(event.body || '{}');
  const out = {
    worker_id: workerId, function_id: process.env.ORVA_FUNCTION_ID,
    exposed: Object.keys(process.env).filter(k => reserved.includes(k.toUpperCase())).sort(),
    fd: process.env.ORVA_SDK_FD || null, proto: process.env.ORVA_SDK_PROTO || null,
    ordinary_env: process.env.PERMISSION_TEST_ENV || null,
  };
  if (q.op === 'invoke') {
    out.result = await orva.invoke(q.target, {value: q.value});
  } else {
    try {
      await orva.kv.put('permission-probe', q.value);
      out.value = await orva.kv.get('permission-probe');
      out.kv_ok = true;
    } catch (e) {
      out.kv_ok = false;
      out.error_name = e.name;
      out.error = String(e.message);
    }
  }
  return {statusCode: 200, body: JSON.stringify(out)};
};
""",
    "python": r"""
import json
import os
import uuid
import orva

worker_id = uuid.uuid4().hex
reserved = {'ORVA_SDK_FD', 'ORVA_SDK_PROTO', 'ORVA_INTERNAL_TOKEN', 'ORVA_API_BASE'}

def handler(event):
    q = json.loads(event.get('body') or '{}')
    out = {
        'worker_id': worker_id, 'function_id': os.environ.get('ORVA_FUNCTION_ID'),
        'exposed': sorted(k for k in os.environ if k.upper() in reserved),
        'fd': os.environ.get('ORVA_SDK_FD'), 'proto': os.environ.get('ORVA_SDK_PROTO'),
        'ordinary_env': os.environ.get('PERMISSION_TEST_ENV'),
    }
    if q.get('op') == 'invoke':
        out['result'] = orva.invoke(q['target'], {'value': q['value']})
    else:
        try:
            orva.kv.put('permission-probe', q['value'])
            out['value'] = orva.kv.get('permission-probe')
            out['kv_ok'] = True
        except Exception as exc:
            out['kv_ok'] = False
            out['error_name'] = type(exc).__name__
            out['error'] = str(exc)
    return {'statusCode': 200, 'body': json.dumps(out)}
""",
}


def wait_for(description, probe, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = probe()
        if result:
            return result
        time.sleep(0.2)
    raise AssertionError('timed out waiting for ' + description)


def records(c, fn, sdk, network, response=None):
    label = fn['name']
    if response is not None:
        check(label + ' mutation response reports permission and network',
              response.get('sdk') is sdk and response.get('network_mode') == network)
    row = c.get(f"/api/v1/functions/{fn['id']}")
    check(label + ' GET persists permission and network',
          row.get('sdk') is sdk and row.get('network_mode') == network)
    rows = c.get('/api/v1/functions?limit=10000')['functions']
    listed = next((r for r in rows if r['id'] == fn['id']), {})
    check(label + ' list reports permission and network',
          listed.get('sdk') is sdk and listed.get('network_mode') == network)


def create(c, owned, suffix, runtime, network, explicit_false=False):
    filename = 'handler.js' if runtime == 'node' else 'handler.py'
    body = {
        'name': f'e2e-sdk-perm-{suffix}-{runtime}-{network}' + ('-false' if explicit_false else ''),
        'description': 'SDK opt-in regression', 'runtime': runtime, 'entrypoint': filename,
        'network_mode': network, 'auth_mode': 'none', 'memory_mb': 64, 'cpus': 0.1,
        'max_concurrency': 1, 'timeout_ms': 10000,
    }
    if explicit_false:
        body['sdk'] = False
        body['env_vars'] = INJECTED
    fn = c.post('/api/v1/functions', body)
    owned.append(fn['id'])
    records(c, fn, False, network, fn)
    _, submitted = c.req('POST', f"/api/v1/functions/{fn['id']}/deploy-inline", {
        'code': SOURCES[runtime], 'filename': filename,
    }, expect=(200, 202))
    warning = submitted.get('warning', '').lower()
    check(fn['name'] + ' deploy warns about disabled SDK, not a network-only requirement',
          'sdk' in warning and 'sdk=true' in warning
          and ('false' in warning or 'disabled' in warning), warning)
    deployment_id = submitted['deployment_id']

    def built():
        row = c.get(f'/api/v1/deployments/{deployment_id}')
        if row['status'] == 'failed':
            raise AssertionError(f"{fn['name']} deployment failed: {row}")
        return (row['status'] in ('succeeded', 'deployed')
                and c.get(f"/api/v1/functions/{fn['id']}")['status'] == 'active')

    wait_for(fn['name'] + ' specific deployment', built)
    return fn


def call(c, fn, value, **extra):
    status, result = c.req('POST', f"/fn/{fn['id']}", {'value': value, **extra},
                           expect=range(100, 600))
    if status != 200 or not isinstance(result, dict):
        raise AssertionError(f"{fn['name']} sandbox response: {status}: {result!r}")
    check(fn['name'] + ' executed its own handler',
          result.get('function_id') == fn['id'] and bool(result.get('worker_id')))
    return result


def disabled(label, result, injected=False):
    check(label + ' gets no reserved SDK environment (including case variants)',
          result.get('exposed') == [] and result.get('fd') is None and result.get('proto') is None,
          str({k: result.get(k) for k in ('exposed', 'fd', 'proto')}))
    message = result.get('error', '').lower()
    check(label + ' SDK call fails clearly rather than crashing or silently succeeding',
          result.get('kv_ok') is False and bool(result.get('error_name'))
          and 'sdk' in message
          and any(word in message for word in ('not available', 'disabled', 'permission', 'missing')),
          str({k: result.get(k) for k in ('kv_ok', 'error_name', 'error')}))
    if injected:
        check(label + ' ordinary author environment survives sanitization',
              result.get('ordinary_env') == 'preserved')


def enabled(c, fn, value):
    result = call(c, fn, value)
    check(fn['name'] + ' SDK KV roundtrip succeeds',
          result.get('kv_ok') is True and result.get('value') == value)
    check(fn['name'] + ' receives real channel fd/protocol',
          result.get('fd') == '3' and result.get('proto') == '1')
    check(fn['name'] + ' receives only channel capability, never legacy token/base',
          result.get('exposed') == ['ORVA_SDK_FD', 'ORVA_SDK_PROTO'],
          str(result.get('exposed')))
    saved = c.get(f"/api/v1/functions/{fn['id']}/kv/permission-probe")
    check(fn['name'] + ' operator observes exact SDK side effect', saved.get('value') == value)
    return result


def toggle(c, fn, sdk, network):
    updated = c.put(f"/api/v1/functions/{fn['id']}", {'sdk': sdk})
    records(c, fn, sdk, network, updated)


def matrix_case(c, fn, network):
    label = fn['name']
    section(label + ' default denied')
    disabled(label + ' default', call(c, fn, 'must-not-write-default'))
    check(label + ' denied SDK wrote nothing',
          c.status('GET', f"/api/v1/functions/{fn['id']}/kv/permission-probe") == 404)

    section(label + ' reserved env cannot grant permission')
    c.put(f"/api/v1/functions/{fn['id']}", {'env_vars': INJECTED})
    disabled(label + ' injected', call(c, fn, 'must-not-write-injected'), injected=True)
    records(c, fn, False, network)
    other_network = 'egress' if network == 'none' else 'none'
    updated = c.put(f"/api/v1/functions/{fn['id']}", {'network_mode': other_network})
    records(c, fn, False, other_network, updated)
    disabled(label + ' network-only change', call(c, fn, 'must-not-write-network'), injected=True)
    c.put(f"/api/v1/functions/{fn['id']}", {'network_mode': network})

    section(label + ' enable without changing network, then revoke warm access')
    toggle(c, fn, True, network)
    first = enabled(c, fn, {'enabled': 1})
    warm = enabled(c, fn, {'enabled': 2})
    check(label + ' successful SDK worker is warm before revocation',
          first['worker_id'] == warm['worker_id'])
    toggle(c, fn, False, network)
    # No retry or grace period: the very next invocation must lose the capability.
    revoked = call(c, fn, 'must-not-write-revoked')
    disabled(label + ' revoked', revoked, injected=True)
    check(label + ' permission toggle replaced the warm worker',
          revoked['worker_id'] != warm['worker_id'])
    saved = c.get(f"/api/v1/functions/{fn['id']}/kv/permission-probe")
    check(label + ' revoked SDK did not change existing KV', saved.get('value') == {'enabled': 2})


def drain(c):
    def drained():
        active = c.get('/api/v1/system/metrics.json')['active_requests']
        writer = c.get('/api/v1/system/health')['writer']
        return active == 0 and all(writer[k] == 0 for k in (
            'critical_queue_bytes', 'activity_queue_bytes', 'telemetry_queue_bytes'))
    wait_for('accepted invocation writes to drain', drained, timeout=30)


def main():
    c = OrvaClient()
    if not c.key:
        print('ORVA_API_KEY not set', file=sys.stderr)
        return 2
    owned = []
    suffix = uuid.uuid4().hex[:12]
    functions, callees = {}, {}
    try:
        for runtime in ('node', 'python'):
            for network in ('none', 'egress'):
                try:
                    fn = create(c, owned, suffix, runtime, network)
                    functions[runtime, network] = fn
                    matrix_case(c, fn, network)
                except Exception as exc:
                    check(f'{runtime}/{network} permission journey', False, str(exc))
            try:
                fn = create(c, owned, suffix, runtime, 'none', explicit_false=True)
                callees[runtime] = fn
                disabled(runtime + ' explicit false at creation',
                         call(c, fn, 'must-not-write-explicit-false'), injected=True)
            except Exception as exc:
                check(runtime + ' explicit-false fixture', False, str(exc))

        section('SDK-enabled callers do not confer SDK authority on callees')
        for runtime in ('node', 'python'):
            try:
                caller = functions[runtime, 'none']
                toggle(c, caller, True, 'none')
                enabled(c, caller, 'caller-still-enabled')
                for callee_runtime in ('node', 'python'):
                    callee = callees[callee_runtime]
                    result = call(c, caller, 'callee-must-not-write', op='invoke',
                                  target=callee['name'])['result']
                    check(f'{runtime} can invoke SDK-disabled {callee_runtime}',
                          result.get('statusCode') == 200
                          and result.get('body', {}).get('function_id') == callee['id'])
                    disabled(f'{runtime} -> {callee_runtime} callee', result['body'], injected=True)
                    check(callee['name'] + ' F2F did not grant SDK write access',
                          c.status('GET', f"/api/v1/functions/{callee['id']}/kv/permission-probe") == 404)
                    records(c, callee, False, 'none')
            except Exception as exc:
                check(runtime + ' SDK-enabled to disabled F2F journey', False, str(exc))
    except Exception as exc:
        check('SDK permission fixture setup', False, str(exc))
    finally:
        if owned:
            try:
                drain(c)
                check('accepted invocation writes drained before cleanup', True)
            except Exception as exc:
                check('writer drain before cleanup', False, str(exc))
        for fid in reversed(owned):
            try:
                c.req('DELETE', f'/api/v1/functions/{fid}', expect=(200, 204, 404))
                check('owned permission function removed ' + fid,
                      c.status('GET', f'/api/v1/functions/{fid}') == 404)
            except Exception as exc:
                check('owned permission function cleanup ' + fid, False, str(exc))
    return summary()


if __name__ == '__main__':
    sys.exit(main())
