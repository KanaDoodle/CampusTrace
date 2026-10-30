#!/usr/bin/env python3
"""Verify real API/Worker/KanaRPC traces, metrics, provisioned UI and observer loss."""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

work, evidence, project = Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3]
if not project.startswith('ct-observer-test-'):
    raise SystemExit('Only an exclusively owned disposable stack is allowed')
addresses = {name: 'http://' + (work / (name + '.address')).read_text().strip()
             for name in ('api', 'prometheus', 'tempo', 'grafana')}
assert all(value.startswith('http://127.0.0.1:') for value in addresses.values())
compose = ['docker', 'compose', '--project-name', project, '--file', str(work / 'compose.json')]
token = ''


def request(service, path, body=None, timeout=5):
    headers = {'Accept': 'application/json'}
    if service == 'api' and token:
        headers['Authorization'] = 'Bearer ' + token
    if body is not None:
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request(addresses[service] + path,
                                 data=None if body is None else json.dumps(body).encode(), headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            raw = response.read()
            return json.loads(raw), response.headers
    except urllib.error.HTTPError as error:
        raise urllib.error.URLError(f'{service} HTTP {error.code}: {error.read().decode(errors="replace")[:500]}') from error


def eventually(label, function, seconds=40):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            value = function()
            if value:
                return value
        except (urllib.error.URLError, TimeoutError, ValueError, KeyError, AssertionError) as error:
            last = str(error)
        time.sleep(0.3)
    raise AssertionError(f'{label} did not converge: {last}')


def ingest(marker):
    started = time.monotonic()
    observation, headers = request('api', '/api/ingest', {
        'company': 'Synthetic Observability', 'title': 'Go 服务端开发', 'job_type': 'FULL_TIME',
        'external_id': uuid.uuid4().hex, 'locations': ['上海'],
        'text': '任职要求：熟悉 Go，2027 届毕业生。工作职责：开发后端服务。' + marker,
        'fetch_status': 'SUCCESS'})
    elapsed = time.monotonic() - started
    trace_id = headers['X-Trace-ID']
    assert len(trace_id) == 32

    def completed():
        job, _ = request('api', '/api/jobs/' + observation['job_id'])
        return any(row['id'] == observation['id'] and row['extraction_status'] == 'COMPLETE'
                   for row in job['observations'])

    eventually('background RPC result', completed)
    return trace_id, elapsed


def trace_spans(trace_id):
    data, _ = request('tempo', '/api/traces/' + trace_id)
    spans = []
    for batch in data.get('batches', data.get('resourceSpans', [])):
        attrs = {a['key']: a.get('value', {}).get('stringValue') for a in batch.get('resource', {}).get('attributes', [])}
        for scope in batch.get('scopeSpans', batch.get('instrumentationLibrarySpans', [])):
            for span in scope.get('spans', []):
                spans.append((attrs.get('service.name'), span))
    return data, spans


def assert_trace(trace_id):
    data, spans = trace_spans(trace_id)
    names = {span['name'] for _, span in spans}
    required = {'POST /api/ingest', 'outbox.enqueue', 'queue.publish', 'task.process', 'rpc.AnalyzeObservation'}
    if not required <= names:
        return False
    assert {'campustrace-api', 'campustrace-worker', 'campustrace-analysis'} <= {service for service, _ in spans}
    index = {span['spanId']: (service, span) for service, span in spans}
    server = next(span for service, span in spans if service == 'campustrace-analysis' and span['name'] == 'rpc.AnalyzeObservation')
    client_service, client = index[server['parentSpanId']]
    assert client_service == 'campustrace-worker' and client['name'] == 'rpc.AnalyzeObservation'
    consumers = [span for _, span in spans if span['name'] == 'task.process']
    assert any(index.get(span.get('parentSpanId'), ('', {}))[1].get('name') == 'queue.publish' for span in consumers)
    publishers = [span for _, span in spans if span['name'] == 'queue.publish']
    assert any(index.get(span.get('parentSpanId'), ('', {}))[1].get('name') == 'outbox.enqueue' for span in publishers)
    raw = json.dumps(data, ensure_ascii=False)
    for secret in ('never-export-private-marker', 'Authorization', 'synthetic-observer-test-secret', 'db.statement'):
        assert secret not in raw, 'private payload leaked into trace'
    (evidence / ('trace-' + trace_id + '.json')).write_text(json.dumps(data, ensure_ascii=False, indent=2))
    return {'trace_id': trace_id, 'spans': len(spans), 'services': sorted({service for service, _ in spans})}


account = {'email': 'observer-' + uuid.uuid4().hex + '@synthetic.invalid', 'password': 'Synthetic2026!'}
request('api', '/auth/register', account)
login, _ = request('api', '/auth/login', account)
token = login['token']
first, first_latency = ingest('never-export-private-marker')
trace = eventually('continuous cross-process trace', lambda: assert_trace(first))


def targets_ready():
    data, _ = request('prometheus', '/api/v1/query?' + urllib.parse.urlencode({'query': 'up{job=~"campustrace-.*"}'}))
    rows = data['data']['result']
    return rows if len(rows) == (4 if os.environ.get('CAMPUS_SMOKE_KAFKA')=='1' else 3 if os.environ.get('CAMPUS_SMOKE_SEARCH')=='1' else 2) and all(row['value'][1] == '1' for row in rows) else False


targets = eventually('Prometheus API and Worker scraping', targets_ready)
rules, _ = request('prometheus', '/api/v1/rules')
assert sum(len(group['rules']) for group in rules['data']['groups']) == 4
dashboard, _ = request('grafana', '/api/dashboards/uid/campustrace-overview')
assert len(dashboard['dashboard']['panels']) == 14
def grafana_query_ready():
    proxy, _ = request('grafana', '/api/datasources/proxy/uid/campustrace-prometheus/api/v1/query?' + urllib.parse.urlencode({'query': 'campustrace_queue_capacity'}))
    assert len(proxy['data']['result']) == 10
    return True


eventually('Grafana provisioned datasource query', grafana_query_ready)
subprocess.run(compose + ['stop', '-t', '10', 'tempo'], check=True, stdout=subprocess.DEVNULL)
while_down, down_latency = ingest('collector-unavailable-synthetic')
assert down_latency < 3, 'trace export blocked the business request'
subprocess.run(compose + ['start', 'tempo'], check=True, stdout=subprocess.DEVNULL)
# Docker can assign a new host port when a container published port 0. Query it
# again rather than mistaking the old proxy address for a collector failure.
addresses['tempo'] = 'http://' + subprocess.check_output(compose + ['port', 'tempo', '3200'], text=True).strip()


def tempo_ready():
    with urllib.request.urlopen(addresses['tempo'] + '/ready', timeout=3) as response:
        return response.status == 200


eventually('Tempo readiness after restart', tempo_ready)
recovered, recovery_latency = ingest('collector-recovered-synthetic')
recovered_trace = eventually('trace export after Tempo restart', lambda: assert_trace(recovered))
result = {'kind': 'local-disposable-controlled-acceptance', 'external_model_calls': 0,
          'first_trace': trace, 'recovered_trace': recovered_trace,
          'prometheus_targets': [row['metric']['job'] for row in targets],
          'dashboard_panels': 14, 'alert_rules': 4, 'queue_lanes': 10,
          'observer_outage_business_completed': True,
          'api_ingest_seconds': {'first': first_latency, 'observer_down': down_latency, 'recovered': recovery_latency},
          'limits': 'Single-host synthetic inputs. This is not a production latency, HA, privacy proof or model-quality benchmark.'}
(evidence / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(result, ensure_ascii=False, indent=2))
