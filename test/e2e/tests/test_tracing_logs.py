#!/usr/bin/env python3
"""Real SDK logs across HTTP, buffered/streaming F2F and jobs, in both runtimes.

Sandbox/build failures are failures, never skips. Also checks signed inbound
webhooks and replay. Cron uses the same scheduler recording path as jobs.
"""
import json
import hashlib
import hmac
import re
import sys
import time
import uuid
import urllib.request

from harness import OrvaClient, check, section, summary


NODE_COMMON = r"""
const orva = require('orva');
function emit(label) {
  console.log('stdout:' + label);
  console.error('stderr:' + label);
  console.error('__ORVA_LOG_JSON__not-json');
  orva.log.info(label, {label, runtime: 'node'});
}
"""
PY_COMMON = r"""
import json
import sys
import orva

def emit(label):
    print('stdout:' + label, flush=True)
    print('stderr:' + label, file=sys.stderr, flush=True)
    print('__ORVA_LOG_JSON__not-json', file=sys.stderr, flush=True)
    orva.log.info(label, {'label': label, 'runtime': 'python'})
"""

SOURCES = {
    'node': {
        'buffered': NODE_COMMON + r"""
exports.handler = async event => {
  const p = JSON.parse(event.body);
  emit(p.label);
  if (p.label === 'failed') {
    console.error('child-source-error');
    return {statusCode: 500, body: 'child-source-error'};
  }
  return {statusCode: 200, body: 'buffered-body'};
};
""",
        'streamed': NODE_COMMON + r"""
exports.handler = async function* () {
  emit('streamed');
  yield 'stream-';
  yield 'body';
  orva.log.info('streamed-end', {label: 'streamed-end', runtime: 'node'});
};
""",
        'job': NODE_COMMON + r"""
exports.handler = async () => {
  emit('job');
  return {statusCode: 200, body: 'job-body'};
};
""",
        'root': NODE_COMMON + r"""
exports.handler = async event => {
  const p = JSON.parse(event.body);
  emit('root');
  const buffered = await orva.invoke(p.buffered, {label: 'buffered'});
  const failed = await orva.invoke(p.buffered, {label: 'failed'});
  const chunks = [];
  for await (const chunk of orva.invokeStream(p.streamed, {})) {
    chunks.push(Buffer.from(chunk));
  }
  const job = await orva.jobs.enqueue(p.job, {}, {maxAttempts: 1});
  return {statusCode: 200, body: JSON.stringify({
    buffered_status: buffered.statusCode, buffered: buffered.body,
    failed_status: failed.statusCode, failed: failed.body,
    streamed: Buffer.concat(chunks).toString(), job_id: job.id,
    execution_id: event.headers['x-orva-execution-id'],
    trace_id: event.headers['x-orva-trace-id'],
    span_id: event.headers['x-orva-span-id']
  })};
};
""",
    },
    'python': {
        'buffered': PY_COMMON + r"""
def handler(event):
    p = json.loads(event['body'])
    emit(p['label'])
    if p['label'] == 'failed':
        print('child-source-error', file=sys.stderr, flush=True)
        return {'statusCode': 500, 'body': 'child-source-error'}
    return {'statusCode': 200, 'body': 'buffered-body'}
""",
        'streamed': PY_COMMON + r"""
def handler(event):
    emit('streamed')
    yield 'stream-'
    yield 'body'
    orva.log.info('streamed-end', {'label': 'streamed-end', 'runtime': 'python'})
""",
        'job': PY_COMMON + r"""
def handler(event):
    emit('job')
    return {'statusCode': 200, 'body': 'job-body'}
""",
        'root': PY_COMMON + r"""
def handler(event):
    p = json.loads(event['body'])
    emit('root')
    buffered = orva.invoke(p['buffered'], {'label': 'buffered'})
    failed = orva.invoke(p['buffered'], {'label': 'failed'})
    streamed = b''.join(orva.invoke_stream(p['streamed'], {})).decode()
    job = orva.jobs.enqueue(p['job'], {}, max_attempts=1)
    return {'statusCode': 200, 'body': json.dumps({
        'buffered_status': buffered['statusCode'], 'buffered': buffered['body'],
        'failed_status': failed['statusCode'], 'failed': failed['body'],
        'streamed': streamed, 'job_id': job['id'],
        'execution_id': event['headers']['x-orva-execution-id'],
        'trace_id': event['headers']['x-orva-trace-id'],
        'span_id': event['headers']['x-orva-span-id']
    })}
""",
    },
}


def clean(stderr):
    """Drop Node/abseil runtime noise some hosts emit; it is not handler output."""
    return ''.join(line for line in stderr.splitlines(True) if not line.startswith('[mutex.cc'))


def wait_for(description, probe, timeout=30):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = probe()
        if value:
            return value
        time.sleep(0.2)
    raise AssertionError('timed out waiting for ' + description
                         + ' (last: ' + str(getattr(probe, 'last', None)) + ')')


def deploy(c, owned, runtime, role, suffix):
    filename = 'handler.js' if runtime == 'node' else 'handler.py'
    fn = c.post('/api/v1/functions', {
        'name': f'e2e-trace-logs-{suffix}-{runtime}-{role}', 'runtime': runtime,
        'entrypoint': filename, 'memory_mb': 128, 'timeout_ms': 30000,
        'cpus': 1, 'network_mode': 'egress' if role == 'root' else 'none', 'auth_mode': 'none',
    })
    owned.append(fn['id'])
    _, submitted = c.req('POST', f"/api/v1/functions/{fn['id']}/deploy-inline",
                         {'code': SOURCES[runtime][role], 'filename': filename},
                         expect=(200, 202))
    deployment_id = submitted.get('deployment_id')

    def built():
        if deployment_id:
            row = c.get(f'/api/v1/deployments/{deployment_id}')
            if row.get('status') == 'failed':
                raise AssertionError(f'{runtime} {role} deployment failed')
            if row.get('status') not in ('succeeded', 'deployed'):
                return False
        return c.get(f"/api/v1/functions/{fn['id']}").get('status') == 'active'

    wait_for(f'{runtime} {role} deployment', built, timeout=90)
    return fn


def verify_trace(c, runtime, functions, result):
    trace_id = result['trace_id']
    check(f'{runtime} trace ID is valid', bool(re.fullmatch('tr_[0-9a-f]{32}', trace_id)))
    labels = {'root', 'buffered', 'failed', 'streamed', 'streamed-end', 'job'}

    def collected():
        status, trace = c.req('GET', f'/api/v1/traces/{trace_id}', expect=(200, 404))
        got = {e['message'] for e in trace.get('log_entries') or []} if status == 200 else set()
        collected.last = {'status': status, 'spans': len((trace or {}).get('spans') or []),
                          'missing': sorted(labels - got)}
        if status != 200 or len(trace.get('spans') or []) < 5:
            return False
        if not labels.issubset(got):
            return False
        return trace

    trace = wait_for(f'{runtime} all child logs in trace detail', collected)
    entries = trace['log_entries']
    spans = {s['execution_id']: s for s in trace['spans']}
    check(f'{runtime} exactly five execution spans', len(spans) == 5)
    check(f'{runtime} no missing or duplicate structured logs',
          len(entries) == len(labels) and {e['message'] for e in entries} == labels)
    root = spans[result['execution_id']]
    check(f'{runtime} root attribution', root['function_id'] == functions['root']['id']
          and root['span_id'] == result['span_id'] and root['trigger'] == 'http')
    by_label = {e['message']: e for e in entries}
    check(f'{runtime} end-of-stream log stays with its execution',
          by_label['streamed-end']['execution_id'] == by_label['streamed']['execution_id'])
    for label in sorted(labels):
        entry = by_label[label]
        span = spans[entry['execution_id']]
        role = 'buffered' if label in ('buffered', 'failed') else label.split('-')[0]
        check(f'{runtime} {label} fields and execution/span attribution',
              entry['level'] == 'info'
              and json.loads(entry.get('fields') or '{}') == {'label': label, 'runtime': runtime}
              and entry['span_id'] == span['span_id']
              and span['function_id'] == functions[role]['id'])
        if label != 'root':
            check(f'{runtime} {label} preserves parent context',
                  span.get('parent_span_id') == root['span_id']
                  and span.get('parent_function_id') == functions['root']['id']
                  and span['trigger'] == ('job' if role == 'job' else 'f2f'))
        if label == 'streamed-end':
            continue
        check(f'{runtime} {label} outcome',
              span['status'] == ('error' if label == 'failed' else 'success')
              and span['status_code'] == (500 if label == 'failed' else 200))
        expected = f'stdout:{label}\nstderr:{label}\n__ORVA_LOG_JSON__not-json\n'
        if label == 'failed':
            expected += 'child-source-error\n'

        def logs_ready():
            status, logs = c.req('GET', f"/api/v1/executions/{entry['execution_id']}/logs",
                                 expect=(200, 404))
            count = 2 if label == 'streamed' else 1
            return logs if status == 200 and len(logs.get('log_entries') or []) >= count else False

        logs = wait_for(f'{runtime} {label} execution logs', logs_ready)
        check(f'{runtime} {label} exact raw output and malformed frame preserved',
              clean(logs['stderr']) == expected and logs['stdout'] == '',
              f"stderr={logs['stderr']!r} stdout={logs['stdout']!r}")
        expected_entries = [by_label['streamed'], by_label['streamed-end']] if label == 'streamed' else [entry]
        check(f'{runtime} {label} execution and trace logs agree exactly once',
              sorted(e['id'] for e in logs['log_entries']) == sorted(e['id'] for e in expected_entries))


def drain(c):
    def drained():
        active = c.get('/api/v1/system/metrics.json')['active_requests']
        writer = c.get('/api/v1/system/health')['writer']
        return active == 0 and all(writer[k] == 0 for k in (
            'critical_queue_bytes', 'activity_queue_bytes', 'telemetry_queue_bytes'))
    wait_for('active invocations and writer bytes to drain', drained, timeout=15)


def direct_post(c, path, body, headers=None):
    data = json.dumps(body).encode()
    req = urllib.request.Request(c.base + path, data=data, method='POST',
                                 headers={**c._headers(), **(headers or {})})
    with urllib.request.urlopen(req, timeout=45) as response:
        return response.status, response.read().decode(), response.headers['X-Orva-Execution-ID']


def verify_direct_logs(c, runtime, exec_id, label, trigger):
    def collected():
        status, row = c.req('GET', f'/api/v1/executions/{exec_id}', expect=(200, 404))
        code, logs = c.req('GET', f'/api/v1/executions/{exec_id}/logs', expect=(200, 404))
        return (row, logs) if status == code == 200 and logs.get('log_entries') else False
    row, logs = wait_for(runtime + ' ' + trigger + ' logs', collected)
    check(f'{runtime} {trigger} structured log attributed exactly once',
          len(logs['log_entries']) == 1 and logs['log_entries'][0]['message'] == label
          and logs['execution_id'] == exec_id
          and logs['log_entries'][0]['span_id'] == row['span_id']
          and row['trigger'] == trigger)
    check(f'{runtime} {trigger} ordinary and malformed stderr preserved',
          clean(logs['stderr']) == f'stdout:{label}\nstderr:{label}\n__ORVA_LOG_JSON__not-json\n')
    detail = c.get(f"/api/v1/traces/{row['trace_id']}")
    check(f'{runtime} {trigger} structured log visible in trace',
          [e['id'] for e in detail['log_entries']] == [e['id'] for e in logs['log_entries']])


def verify_inbound_and_replay(c, runtime, fn):
    label = 'direct-http'
    status, body, original = direct_post(c, f"/fn/{fn['id']}", {'label': label})
    check(runtime + ' direct HTTP response', (status, body) == (200, 'buffered-body'))
    verify_direct_logs(c, runtime, original, label, 'http')
    wait_for('original request captured', lambda: c.req(
        'GET', f'/api/v1/executions/{original}/request', expect=(200, 404))[0] == 200)
    status, body, replay = direct_post(c, f'/api/v1/executions/{original}/replay', {})
    check(runtime + ' replay response and fresh execution',
          (status, body) == (200, 'buffered-body') and replay != original)
    verify_direct_logs(c, runtime, replay, label, 'replay')
    hook = c.post(f"/api/v1/functions/{fn['id']}/inbound-webhooks", {
        'name': 'trace-log-probe', 'signature_format': 'hmac_sha256_hex',
    })
    payload = {'label': 'signed-inbound'}
    signature = hmac.new(hook['secret'].encode(), json.dumps(payload).encode(), hashlib.sha256).hexdigest()
    status, body, inbound = direct_post(c, hook['trigger_url'], payload, {'X-Orva-Signature': signature})
    check(runtime + ' signed inbound response', (status, body) == (200, 'buffered-body'))
    verify_direct_logs(c, runtime, inbound, payload['label'], 'inbound')


def main():
    c = OrvaClient()
    if not c.key:
        print('ORVA_API_KEY not set', file=sys.stderr)
        return 2
    owned = []
    suffix = uuid.uuid4().hex[:12]
    try:
        for runtime in ('node', 'python'):
            section(runtime + ' HTTP -> F2F buffered/failed/streamed + job logs')
            functions = {role: deploy(c, owned, runtime, role, suffix)
                         for role in ('buffered', 'streamed', 'job', 'root')}
            status, result = c.req('POST', f"/fn/{functions['root']['id']}",
                                 {role: functions[role]['name'] for role in ('buffered', 'streamed', 'job')})
            assert status == 200 and isinstance(result, dict), 'root did not return its fixture response'
            check(runtime + ' actual child responses including source error retained',
                  {k: result.get(k) for k in ('buffered_status', 'buffered', 'failed_status', 'failed', 'streamed')}
                  == {'buffered_status': 200, 'buffered': 'buffered-body',
                      'failed_status': 500, 'failed': 'child-source-error', 'streamed': 'stream-body'})

            def job_finished():
                job = c.get(f"/api/v1/jobs/{result['job_id']}")
                if job.get('status') in ('failed', 'dead'):
                    raise AssertionError(runtime + ' job failed')
                return job.get('status') in ('completed', 'succeeded')

            wait_for(runtime + ' job completed', job_finished)
            verify_trace(c, runtime, functions, result)
            verify_inbound_and_replay(c, runtime, functions['buffered'])
    except Exception as exc:
        check('tracing log execution journey completed', False, str(exc))
    finally:
        try:
            drain(c)
            check('accepted invocation writes drained before cleanup', True)
        except Exception as exc:
            check('writer drain observed', False, str(exc))
        for fid in reversed(owned):
            try:
                jobs = c.get(f'/api/v1/jobs?function_id={fid}').get('jobs') or []
                for job in jobs:
                    c.req('DELETE', f"/api/v1/jobs/{job['id']}", expect=(200, 204, 404))
            except Exception as exc:
                check('owned job cleanup', False, str(exc))
            try:
                c.req('DELETE', f'/api/v1/functions/{fid}', expect=(200, 204, 404))
            except Exception as exc:
                check('owned function cleanup', False, str(exc))
    return summary()


if __name__ == '__main__':
    sys.exit(main())
