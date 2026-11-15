#!/usr/bin/env python3
"""Real network_mode:none SDK invoke/stream/jobs/cron/span journeys in both runtimes.

Sandbox failures fail, never skip (including with ORVA_REQUIRE_SANDBOX=1).
Only this run's uniquely named functions and their children are cleaned up.
"""
import base64
import json
import os
import sys
import time
import uuid

from harness import OrvaClient, check, latest_execution_stderr, section, summary


NODE_COMMON = r"""
const orva = require('orva');
const ok = body => ({statusCode: 200, body: JSON.stringify(body)});
const meta = () => ({
  execution_id: orva.context.executionId, trace_id: orva.context.traceId,
  span_id: orva.context.spanId, depth: orva.context.callDepth,
  fd: process.env.ORVA_SDK_FD, proto: process.env.ORVA_SDK_PROTO,
});
"""
PY_COMMON = r"""
import json
import os
import time
import orva

def ok(body):
    return {'statusCode': 200, 'body': json.dumps(body)}

def meta():
    return {
        'execution_id': orva.context.execution_id, 'trace_id': orva.context.trace_id,
        'span_id': orva.context.span_id, 'depth': orva.context.call_depth,
        'fd': os.environ.get('ORVA_SDK_FD'), 'proto': os.environ.get('ORVA_SDK_PROTO'),
    }
"""

SOURCES = {
    'node': {
        'target': NODE_COMMON + r"""
exports.handler = async event => {
  const p = JSON.parse(event.body);
  if (p.op === 'job') {
    const runs = await orva.kv.incr('runs-' + p.marker, 1);
    await orva.kv.put('effect-' + p.marker, {
      payload: p.value, runs, ...meta(), trigger: event.headers['x-orva-trigger'],
    });
    return ok({runs});
  }
  return {statusCode: 201, headers: {'x-channel-echo': 'node'}, body: JSON.stringify({
    payload: p, runtime: 'node', raw_body_is_string: typeof event.body === 'string',
  })};
};
""",
        'stream': NODE_COMMON + r"""
exports.handler = async function* (event) {
  const p = JSON.parse(event.body);
  for (let i = 0; i < 5; i++) {
    yield p.marker + ':' + i + '\n';
    await new Promise(resolve => setTimeout(resolve, 50));
  }
};
""",
        'caller': NODE_COMMON + r"""
exports.handler = async event => {
  const q = JSON.parse(event.body);
  switch (q.op) {
    case 'probe': return ok(meta());
    case 'invoke': return ok({...meta(), result: await orva.invoke(q.target, q.payload)});
    case 'stream': {
      const full = [];
      for await (const chunk of orva.invokeStream(q.target, {marker: q.marker})) {
        full.push({text: Buffer.from(chunk).toString('utf8'), bytes: chunk instanceof Uint8Array});
      }
      const early = [];
      // Exceed the eight-handle cap within ONE lease if break fails to close.
      for (let i = 0; i < 10; i++) {
        for await (const chunk of orva.invokeStream(q.target, {marker: q.marker})) {
          early.push(Buffer.from(chunk).toString('utf8'));
          break;
        }
      }
      const after = [];
      for await (const chunk of orva.invokeStream(q.target, {marker: q.marker})) {
        after.push(Buffer.from(chunk).toString('utf8'));
      }
      await orva.kv.put('after-close', q.marker);
      return ok({...meta(), full, early, after, kv: await orva.kv.get('after-close')});
    }
    case 'jobs': {
      const opts = {maxAttempts: 1, idempotencyKey: q.marker, idempotencyWindowSeconds: 120};
      const payload = {op: 'job', marker: q.marker, value: q.value};
      const first = await orva.jobs.enqueue(q.target, payload, opts);
      const second = await orva.jobs.enqueue(q.target, payload, opts);
      return ok({...meta(), first, second});
    }
    case 'cron': {
      const first = await orva.crons.upsert(q.marker, '0 3 * * *', {
        enabled: false, timezone: 'UTC', payload: {revision: 1},
      });
      const second = await orva.crons.upsert(q.marker, '0 4 * * *', {
        enabled: false, timezone: 'UTC', payload: {revision: 2, marker: q.marker},
      });
      return ok({...meta(), first, second});
    }
    case 'span': {
      const value = await orva.trace.span(q.marker, async () => {
        await orva.kv.put('span-value', q.value);
        return await orva.kv.get('span-value');
      }, {runtime: 'node', marker: q.marker});
      // Await a channel round-trip while the fire-and-forget span still has a live lease.
      await orva.kv.get('span-value');
      return ok({...meta(), value});
    }
    case 'error': {
      try {
        if (q.stream) {
          for await (const chunk of orva.invokeStream(q.target, {})) void chunk;
        } else {
          await orva.invoke(q.target, {});
        }
        return ok({unexpected_success: true});
      } catch (e) {
        return ok({status: e.status, name: e.name, message: String(e.message)});
      }
    }
    case 'recurse': {
      const depth = orva.context.callDepth;
      let result;
      try {
        result = await orva.invoke(q.target, q, {timeoutMs: 45000});
      } catch (e) {
        if (e.status !== 507) throw e;
        return {statusCode: 507, body: JSON.stringify({
          depths: [depth], nested_statuses: [], rejected_at: depth, error_status: e.status,
        })};
      }
      return {statusCode: result.statusCode, body: JSON.stringify({
        ...result.body, depths: [depth, ...result.body.depths],
        nested_statuses: [result.statusCode, ...result.body.nested_statuses],
      })};
    }
    default: throw new Error('unknown fixture op: ' + q.op);
  }
};
""",
    },
    'python': {
        'target': PY_COMMON + r"""
def handler(event):
    p = json.loads(event['body'])
    if p.get('op') == 'job':
        runs = orva.kv.incr('runs-' + p['marker'], 1)
        orva.kv.put('effect-' + p['marker'], {
            'payload': p['value'], 'runs': runs, **meta(),
            'trigger': event['headers']['x-orva-trigger'],
        })
        return ok({'runs': runs})
    return {'statusCode': 201, 'headers': {'x-channel-echo': 'python'}, 'body': json.dumps({
        'payload': p, 'runtime': 'python', 'raw_body_is_string': isinstance(event['body'], str),
    })}
""",
        'stream': PY_COMMON + r"""
def handler(event):
    p = json.loads(event['body'])
    for i in range(5):
        yield p['marker'] + ':' + str(i) + '\n'
        time.sleep(0.05)
""",
        'caller': PY_COMMON + r"""
def handler(event):
    q = json.loads(event['body'])
    op = q['op']
    if op == 'probe':
        return ok(meta())
    if op == 'invoke':
        return ok({**meta(), 'result': orva.invoke(q['target'], q['payload'])})
    if op == 'stream':
        full = [{'text': c.decode('utf-8'), 'bytes': isinstance(c, bytes)}
                for c in orva.invoke_stream(q['target'], {'marker': q['marker']})]
        early = []
        for _ in range(10):
            stream = orva.invoke_stream(q['target'], {'marker': q['marker']})
            try:
                early.append(next(stream).decode('utf-8'))
            finally:
                stream.close()
        after = [c.decode('utf-8') for c in orva.invoke_stream(q['target'], {'marker': q['marker']})]
        orva.kv.put('after-close', q['marker'])
        return ok({**meta(), 'full': full, 'early': early, 'after': after,
                   'kv': orva.kv.get('after-close')})
    if op == 'jobs':
        opts = {'max_attempts': 1, 'idempotency_key': q['marker'], 'idempotency_window_seconds': 120}
        payload = {'op': 'job', 'marker': q['marker'], 'value': q['value']}
        first = orva.jobs.enqueue(q['target'], payload, **opts)
        second = orva.jobs.enqueue(q['target'], payload, **opts)
        return ok({**meta(), 'first': first, 'second': second})
    if op == 'cron':
        first = orva.crons.upsert(q['marker'], '0 3 * * *', enabled=False,
                                  timezone='UTC', payload={'revision': 1})
        second = orva.crons.upsert(q['marker'], '0 4 * * *', enabled=False,
                                   timezone='UTC', payload={'revision': 2, 'marker': q['marker']})
        return ok({**meta(), 'first': first, 'second': second})
    if op == 'span':
        with orva.trace.span(q['marker'], {'runtime': 'python', 'marker': q['marker']}):
            orva.kv.put('span-value', q['value'])
            value = orva.kv.get('span-value')
        return ok({**meta(), 'value': value})
    if op == 'error':
        try:
            if q['stream']:
                for _ in orva.invoke_stream(q['target'], {}):
                    pass
            else:
                orva.invoke(q['target'], {})
            return ok({'unexpected_success': True})
        except orva.OrvaError as exc:
            return ok({'status': exc.status, 'name': type(exc).__name__, 'message': str(exc)})
    if op == 'recurse':
        depth = orva.context.call_depth
        try:
            result = orva.invoke(q['target'], q, timeout_ms=45000)
        except orva.OrvaError as exc:
            if exc.status != 507:
                raise
            return {'statusCode': 507, 'body': json.dumps({
                'depths': [depth], 'nested_statuses': [], 'rejected_at': depth, 'error_status': exc.status,
            })}
        return {'statusCode': result['statusCode'], 'body': json.dumps({
            **result['body'], 'depths': [depth, *result['body']['depths']],
            'nested_statuses': [result['statusCode'], *result['body']['nested_statuses']],
        })}
    raise ValueError('unknown fixture op: ' + op)
""",
    },
}


def wait_for(description, probe, timeout=30):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = probe()
        if last:
            return last
        time.sleep(0.2)
    raise AssertionError(f'timed out waiting for {description} (last: {last!r})')


def create(c, owned, suffix, runtime, role):
    filename = 'handler.js' if runtime == 'node' else 'handler.py'
    fn = c.post('/api/v1/functions', {
        'name': f'e2e-chan-ops-{suffix}-{runtime}-{role}', 'runtime': runtime,
        'entrypoint': filename, 'network_mode': 'none', 'auth_mode': 'none',
        'sdk': role in ('caller', 'target'),
        # Nine simultaneous self-call workers must fit even on a small CI host.
        'memory_mb': 64, 'cpus': 0.1, 'timeout_ms': 45000, 'max_concurrency': 16,
    })
    owned.append(fn['id'])
    if role == 'inactive':
        return fn
    _, submitted = c.req('POST', f"/api/v1/functions/{fn['id']}/deploy-inline", {
        'code': SOURCES[runtime][role], 'filename': filename,
    }, expect=(200, 202))
    deployment_id = submitted['deployment_id']

    def built():
        row = c.get(f'/api/v1/deployments/{deployment_id}')
        if row['status'] == 'failed':
            raise AssertionError(f'{runtime} {role} deployment {deployment_id} failed: {row}')
        return (row['status'] in ('succeeded', 'deployed')
                and c.get(f"/api/v1/functions/{fn['id']}")['status'] == 'active')

    wait_for(f'{runtime} {role} deployment {deployment_id}', built, timeout=90)
    return fn


def call(c, fn, payload, status=200):
    _, result = c.req('POST', f"/fn/{fn['id']}", payload, expect=(status,))
    if not isinstance(result, dict):
        raise AssertionError(f"{fn['name']} did not return fixture JSON: {result!r}")
    return result


def kv(c, fid, key):
    return c.get(f'/api/v1/functions/{fid}/kv/{key}')


def trace_ready(c, trace_id, predicate):
    def collected():
        status, trace = c.req('GET', f'/api/v1/traces/{trace_id}', expect=(200, 404))
        return trace if status == 200 and predicate(trace) else False
    return wait_for('persisted trace ' + trace_id, collected)


def verify_job(c, runtime, caller, target, result, marker, value):
    first, second = result['first'], result['second']
    check(runtime + ' enqueue returns one id, first fresh and second replayed',
          bool(first.get('id')) and first['id'] == second.get('id')
          and first.get('replayed') is False and second.get('replayed') is True, str(result))

    def completed():
        job = c.get(f"/api/v1/jobs/{first['id']}")
        if job['status'] == 'failed':
            raise AssertionError(f'{runtime} actual job failed: {job}')
        return job if job['status'] == 'succeeded' else False

    job = wait_for(runtime + ' actual job completion', completed, timeout=45)
    payload = {'op': 'job', 'marker': marker, 'value': value}
    check(runtime + ' job persisted payload, one attempt and caller attribution',
          job['function_id'] == target['id'] and job['attempts'] == job['max_attempts'] == 1
          and json.loads(base64.b64decode(job['payload'])) == payload
          and job.get('enqueued_by_function_id') == caller['id']
          and job.get('trace_id') == result['trace_id']
          and job.get('parent_span_id') == result['span_id'], str(job))
    rows = c.get(f"/api/v1/jobs?function_id={target['id']}&limit=100")['jobs']
    check(runtime + ' idempotency created exactly one queued row',
          [j['id'] for j in rows
           if json.loads(base64.b64decode(j['payload'])).get('marker') == marker] == [first['id']])
    effect = kv(c, target['id'], 'effect-' + marker)['value']
    check(runtime + ' real job wrote exact KV side effect once',
          effect['payload'] == value and effect['runs'] == 1 and effect['trigger'] == 'job'
          and kv(c, target['id'], 'runs-' + marker)['value'] == 1, str(effect))

    def recorded():
        status, row = c.req('GET', f"/api/v1/executions/{effect['execution_id']}", expect=(200, 404))
        return row if status == 200 else False

    row = wait_for(runtime + ' job execution row', recorded)
    check(runtime + ' actual job execution persisted with parent trace',
          row['function_id'] == target['id'] and row['trigger'] == 'job'
          and row['status'] == 'success' and row['status_code'] == 200
          and row['trace_id'] == result['trace_id'] and row['parent_span_id'] == result['span_id']
          and row['parent_function_id'] == caller['id'], str(row))


def verify_runtime(c, runtime, functions, inactive, suffix):
    caller = functions[runtime]['caller']
    other = 'python' if runtime == 'node' else 'node'
    marker = suffix + '-' + runtime
    value = {'nested': [None, False, True, 0, -7, 1.25, {'text': 'café 雪 "quoted"\n\\'}]}
    section(runtime + ' network-free channel and buffered/cross-runtime invoke')
    probe = call(c, caller, {'op': 'probe'})
    check(runtime + ' real sandbox has channel fd 3 / protocol 1',
          probe.get('fd') == '3' and probe.get('proto') == '1', str(probe))
    check(runtime + ' caller remains network_mode none',
          c.get(f"/api/v1/functions/{caller['id']}")['network_mode'] == 'none')
    for callee_runtime in (runtime, other):
        target = functions[callee_runtime]['target']
        result = call(c, caller, {'op': 'invoke', 'target': target['name'], 'payload': value})['result']
        check(f'{runtime} -> {callee_runtime} preserves status, headers and JSON request/response',
              result.get('statusCode') == 201
              and {k.lower(): v for k, v in result.get('headers', {}).items()}.get('x-channel-echo') == callee_runtime
              and result.get('body') == {'payload': value, 'runtime': callee_runtime, 'raw_body_is_string': True},
              str(result))

    section(runtime + ' five stream frames and explicit early close')
    stream = functions[other]['stream']
    result = call(c, caller, {'op': 'stream', 'target': stream['name'], 'marker': marker})
    chunks = [f'{marker}:{i}\n' for i in range(5)]
    check(runtime + ' invoke_stream yields exactly five ordered byte chunks',
          result['full'] == [{'text': text, 'bytes': True} for text in chunks], str(result['full']))
    check(runtime + ' ten early closes free handles within the same live lease',
          result['early'] == [chunks[0]] * 10 and result['after'] == chunks and result['kv'] == marker,
          str(result))
    trace = trace_ready(c, result['trace_id'], lambda t: len(t.get('spans') or []) >= 13)
    children = [s for s in trace['spans'] if s['function_id'] == stream['id']]
    check(runtime + ' completed and abandoned stream executions persisted exactly once',
          len(children) == 12 and sum(s['status'] == 'success' for s in children) == 2
          and sum(s['status'] == 'error' for s in children) == 10
          and all(s['trigger'] == 'f2f' and s['parent_span_id'] == result['span_id']
                  and s['parent_function_id'] == caller['id'] for s in children))

    section(runtime + ' idempotent job really executes')
    target = functions[other]['target']
    result = call(c, caller, {'op': 'jobs', 'target': target['name'], 'marker': marker, 'value': value})
    verify_job(c, runtime, caller, target, result, marker, value)

    section(runtime + ' disabled cron upsert belongs to the caller')
    result = call(c, caller, {'op': 'cron', 'marker': marker})
    rows = c.get(f"/api/v1/functions/{caller['id']}/cron")['schedules']
    check(runtime + ' cron upsert updates one existing disabled schedule',
          len(rows) == 1 and result['first']['id'] == result['second']['id'] == rows[0]['id'], str(rows))
    row = next(r for r in rows if r['id'] == result['second']['id'])
    check(runtime + ' cron persisted caller, expression, timezone, payload and disabled state',
          row['function_id'] == caller['id'] and row['name'] == marker
          and row['cron_expr'] == '0 4 * * *' and row['timezone'] == 'UTC'
          and row['enabled'] is False and not row.get('last_run_at')
          and json.loads(row['payload']) == {'revision': 2, 'marker': marker}, str(row))

    section(runtime + ' trace.span emission and persistence')
    result = call(c, caller, {'op': 'span', 'marker': marker, 'value': value})
    check(runtime + ' span-wrapped SDK work preserves its result', result['value'] == value)
    trace = trace_ready(c, result['trace_id'], lambda t: bool(t.get('user_spans')))
    spans = trace['user_spans']
    check(runtime + ' exactly one persisted code span with execution, parent and attributes',
          len(spans) == 1 and spans[0]['name'] == marker and spans[0]['status'] == 'ok'
          and spans[0]['execution_id'] == result['execution_id']
          and spans[0]['parent_span_id'] == result['span_id']
          and spans[0]['duration_ms'] >= 0
          and json.loads(spans[0]['attributes']) == {'runtime': runtime, 'marker': marker}, str(spans))

    section(runtime + ' missing and inactive callees fail visibly')
    for name, expected in ((f'e2e-chan-ops-{suffix}-missing', 404), (inactive['name'], 409)):
        for streamed in (False, True):
            result = call(c, caller, {'op': 'error', 'target': name, 'stream': streamed})
            check(f'{runtime} {"stream" if streamed else "buffered"} callee error is {expected}',
                  result.get('status') == expected and result.get('name') == 'OrvaError'
                  and bool(result.get('message')), str(result))

    section(runtime + ' real recursive self-call reaches the host depth cap')
    result = call(c, caller, {'op': 'recurse', 'target': caller['name']}, status=507)
    check(runtime + ' depth 8 rejects the ninth call and nested 507 reaches public HTTP',
          result == {'depths': list(range(9)), 'nested_statuses': [507] * 8,
                     'rejected_at': 8, 'error_status': 507}, str(result))


def drain(c):
    def drained():
        active = c.get('/api/v1/system/metrics.json')['active_requests']
        writer = c.get('/api/v1/system/health')['writer']
        return active == 0 and all(writer[k] == 0 for k in (
            'critical_queue_bytes', 'activity_queue_bytes', 'telemetry_queue_bytes'))
    wait_for('active invocations and all writer byte lanes to drain', drained, timeout=30)


def cleanup(c, owned):
    if not owned:
        return
    try:
        drain(c)
        check('accepted invocation writes drained before cleanup', True)
    except Exception as exc:
        check('writer drain before cleanup', False, str(exc))
    for fid in reversed(owned):
        try:
            jobs = c.get(f'/api/v1/jobs?function_id={fid}&limit=100')['jobs']
            for job in jobs:
                if job['status'] == 'running':
                    wait_for('owned running job to finish', lambda: c.get(
                        f"/api/v1/jobs/{job['id']}")['status'] != 'running', timeout=50)
                c.req('DELETE', f"/api/v1/jobs/{job['id']}", expect=(200, 204, 404))
            for row in c.get(f'/api/v1/functions/{fid}/cron')['schedules']:
                c.req('DELETE', f"/api/v1/functions/{fid}/cron/{row['id']}", expect=(200, 204, 404))
        except Exception as exc:
            check('owned job/cron cleanup ' + fid, False, str(exc))
        try:
            c.req('DELETE', f'/api/v1/functions/{fid}', expect=(200, 204, 404))
            check('owned function removed ' + fid,
                  c.status('GET', f'/api/v1/functions/{fid}') == 404)
        except Exception as exc:
            check('owned function cleanup ' + fid, False, str(exc))


def main():
    c = OrvaClient()
    if not c.key:
        print('ORVA_API_KEY not set', file=sys.stderr)
        return 2
    owned = []
    suffix = uuid.uuid4().hex[:12]
    required = os.environ.get('ORVA_REQUIRE_SANDBOX', '').lower() in ('1', 'true', 'yes')
    try:
        section('deploy owned network_mode:none Node/Python fixtures')
        functions = {runtime: {role: create(c, owned, suffix, runtime, role)
                               for role in ('target', 'stream', 'caller')}
                     for runtime in ('node', 'python')}
        inactive = create(c, owned, suffix, 'node', 'inactive')
        for runtime in ('node', 'python'):
            try:
                verify_runtime(c, runtime, functions, inactive, suffix)
            except Exception as exc:
                check(runtime + ' real sandbox SDK channel operations' + (' (required)' if required else ''),
                      False, str(exc))
                diagnostic = latest_execution_stderr(c, function_id=functions[runtime]['caller']['id'])
                if diagnostic:
                    print(runtime + ' caller stderr: ' + diagnostic[:2000])
    except Exception as exc:
        check('real sandbox fixture setup', False, str(exc))
    finally:
        cleanup(c, owned)
    return summary()


if __name__ == '__main__':
    sys.exit(main())
