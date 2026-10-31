#!/usr/bin/env python3
"""Isolated SQL/Redis and direct-model-API process acceptance. Uses only a local no-model HTTP fixture; no RPC SDK or model calls."""
import argparse
import concurrent.futures
import datetime
import hashlib
import http.cookiejar
import json
import os
import pathlib
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import sys
sys.dont_write_bytecode = True

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--playwright-module', help='absolute installed playwright module path')
    parser.add_argument('--chromium', help='optional Chromium executable')
    args = parser.parse_args()
    repo = pathlib.Path(__file__).resolve().parents[1]
    work = (args.output or pathlib.Path(tempfile.mkdtemp(prefix='campustrace-m4-'))).resolve()
    work.mkdir(parents=True, exist_ok=True)
    processes, containers, handles, checks = ([], [], [], [])
    run_id = uuid.uuid4().hex[:10]
    password = 'm4-synthetic-only'
    env = dict(os.environ, GOWORK='off', SEARCH_URL='', SEARCH_QUERY_TOKEN='', SEARCH_WRITE_TOKEN='', LLM_URL='', LLM_API_KEY='', LLM_MODEL='', OTEL_EXPORTER_OTLP_ENDPOINT='', OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='', OTEL_EXPORTER_OTLP_METRICS_ENDPOINT='', CAMPUS_LIVE_SOURCES='0', VLLM_API_KEY='', KANARPC_FIXTURE_KEY='', NARRATION_BACKEND='disabled', INFERENCE_RPC_ADDRESS='', INFERENCE_MODEL='', INFERENCE_BACKEND_KIND='', JOB_EVENTS_MODE='direct', KAFKA_BROKERS='')

    def run(command, **kwargs):
        return subprocess.run(command, check=True, text=True, capture_output=True, env=env, **kwargs).stdout.strip()

    def wait(check, seconds=15):
        until = time.monotonic() + seconds
        while time.monotonic() < until:
            value = check()
            if value:
                return value
            time.sleep(0.05)
        raise RuntimeError('acceptance condition timed out')

    def launch(name, command, child_env=None):
        out, err = (open(work / (name + '.jsonl'), 'w'), open(work / (name + '.stderr'), 'w'))
        handles.extend([out, err])
        process = subprocess.Popen(command, env=child_env or env, stdout=out, stderr=err)
        processes.append(process)
        return process

    def ready(name, process):

        def check():
            if process.poll() is not None:
                raise RuntimeError(name + ' exited; see ' + str(work / (name + '.stderr')))
            for line in (work / (name + '.jsonl')).read_text().splitlines():
                try:
                    value = json.loads(line)
                except ValueError:
                    continue
                if value.get('event') == 'ready':
                    return value
        return wait(check)

    def get(url):
        with urllib.request.urlopen(url, timeout=5) as response:
            return json.load(response)

    def container(name, image, command, extra):
        full = 'ct-m4-' + name + '-' + run_id
        run(['docker', 'run', '--detach', '--name', full, *extra, image, *command])
        containers.append(full)
        return full

    def mapped(name, port):
        return run(['docker', 'port', name, str(port) + '/tcp']).split(':')[-1]

    def sql(statement):
        return run(['docker', 'exec', mysql, 'mysql', '-uroot', '-p' + password, '-N', '-e', statement])

    class Client:

        def __init__(self):
            self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

        def request(self, path, body=None, status=200, origin=None, headers=None, stream=False, method=None):
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json', 'Origin': origin or base, **(headers or {})}, method=method)
            try:
                response = self.opener.open(req, timeout=10)
            except urllib.error.HTTPError as error:
                response = error
            assert response.status == status, (path, response.status, status, response.read().decode())
            if stream:
                return response
            with response:
                raw = response.read()
                return json.loads(raw) if raw else None

        def register(self, suffix):
            credentials = {'email': 'm4-' + suffix + '-' + run_id + '@example.invalid', 'password': password}
            self.request('/auth/register', credentials)
            self.request('/auth/login', dict(credentials, web_session=True))
            return credentials

    def task(client, task_id):
        return client.request('/api/agent/executions/' + task_id)

    def parent(client):
        value = client.request('/api/agent/executions', {'skill_id': 'daily-review', 'request_key': uuid.uuid4().hex})
        value = wait(lambda: v if (v := task(client, value['id']))['state'] == 'COMPLETED' else None)
        assert value['source_identity'] and value['grounded_observations']
        return value

    def review(client, value):
        return client.request('/api/agent/executions/' + value['id'] + '/narration/preview', {'mask_name': ''})

    def confirm(client, value, preview):
        body = {'request_key': uuid.uuid4().hex, 'review_hash': preview['review_hash'], 'confirm': True, 'mask_name': ''}
        return (client.request('/api/agent/executions/' + value['id'] + '/narration', body), body)

    def partial(client, task_id):
        response = client.request('/api/agent/executions/' + task_id + '/stream', stream=True)
        try:
            event_id = None
            for raw in response:
                line = raw.decode().strip()
                if line.startswith('id: '):
                    event_id = line[4:]
                if line.startswith('data: '):
                    value = json.loads(line[6:])
                    assert value['state'] == 'RUNNING', value
                    if value.get('narration', {}).get('draft'):
                        return (event_id, value)
        finally:
            response.close()
        raise AssertionError('no partial snapshot before terminal')
    try:
        run(['go', 'build', '-mod=readonly', '-o', str(work / 'api'), './cmd/api'], cwd=repo)
        mysql = container('mysql', 'mysql:8.4', [], ['--tmpfs', '/var/lib/mysql', '-p', '127.0.0.1::3306', '-e', 'MYSQL_ROOT_PASSWORD=' + password, '-e', 'MYSQL_DATABASE=ct_m4', '-e', 'MYSQL_USER=m4', '-e', 'MYSQL_PASSWORD=' + password])
        redis = container('redis', 'redis:7.4-alpine', ['redis-server', '--save', '', '--appendonly', 'no'], ['-p', '127.0.0.1::6379'])

        def mysql_ready():
            result = subprocess.run(['docker', 'exec', mysql, 'mysql', '-h127.0.0.1', '-um4', '-p' + password, '-Dct_m4', '-e', 'SELECT 1'], capture_output=True, env=env)
            return result.returncode == 0
        wait(mysql_ready, 90)
        fixture = ready('fixture', launch('fixture', [sys.executable, str(repo / 'scripts/model-api-fixture.py')]))
        fixture_url = 'http://' + fixture['address']
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            http_address = '127.0.0.1:' + str(sock.getsockname()[1])
        base = 'http://' + http_address
        api_env = dict(env, MYSQL_DSN='m4:' + password + '@tcp(127.0.0.1:' + mapped(mysql, 3306) + ')/ct_m4?parseTime=true&loc=UTC', REDIS_ADDR='127.0.0.1:' + mapped(redis, 6379), JWT_SECRET='m4-synthetic-jwt-secret-only-32bytes', HTTP_ADDR=http_address, NARRATION_BACKEND='api', INFERENCE_BACKEND_KIND='fixture-no-model', CAMPUS_REDIS_PREFIX='m4:' + run_id + ':')
        api_env.update(LLM_URL=fixture_url + '/v1/chat/completions', LLM_MODEL='fixture-model', LLM_API_KEY='synthetic-api-fixture-key')

        def api_start(name):
            process = launch(name, [str(work / 'api')], api_env)

            def check():
                if process.poll() is not None:
                    raise RuntimeError(name + ' exited; inspect stderr')
                try:
                    return get(base + '/healthz')
                except (OSError, ValueError):
                    return None
            wait(check, 30)
            return process
        api = api_start('api')
        a, b = (Client(), Client())
        credentials_a, credentials_b = (a.register('a'), b.register('b'))
        # Seed before any parent identity is captured. Only synthetic local
        # materials are used; importing notes performs no embedding/model call.
        b.request('/api/profile', {'target_languages': ['Go'], 'degree': 'MASTER', 'graduation_year': 2027}, method='PUT')
        b.request('/api/agent/todos/settings', {'enabled': True})
        job = b.request('/api/ingest', {'company': '合成准备公司', 'title': '后端工程师', 'external_id': uuid.uuid4().hex, 'job_type': 'FULL_TIME', 'text': 'Go 后端使用 Redis Streams，关注 PEL 和 XAUTOCLAIM 消费恢复。'})
        application = b.request('/api/applications', {'job_id': job['job_id'], 'submitted': True})
        interview = b.request('/api/interviews', {'application_id': application['id'], 'round': 2, 'scheduled_at': (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=2)).isoformat(), 'result': 'PENDING'})
        b.request('/api/reviews', {'interview_id': interview['id'], 'actual_questions': ['XAUTOCLAIM 如何恢复 Redis PEL？'], 'self_evaluation': '需要复习消息重领的边界', 'missed_points': ['Redis PEL 持久化后确认'], 'follow_up_notes': '整理恢复过程与失败边界', 'weak_topics': []})
        b.request('/api/agent/todos')  # Materialize the optional feed before task identities are captured.
        # A synthetic durable job notice exercises inbox read projection, not
        # event delivery. Production notification generation is tested elsewhere.
        notice_id = uuid.uuid4().hex
        notice_owner = b.request('/api/profile/resume/capabilities')['user_id']
        notice = {'id': notice_id, 'user_id': notice_owner, 'type': 'JD_CHANGED', 'entity_type': 'JOB', 'entity_id': job['job_id'], 'title': '合成岗位资料变化', 'body': '请核对关联申请留档', 'created_at': datetime.datetime.now(datetime.timezone.utc).isoformat()}
        notice_hex = json.dumps(notice, ensure_ascii=False).encode().hex()
        sql("INSERT INTO ct_m4.notifications(id,user_id,dedup_key,created_at,body) VALUES('" + notice_id + "','" + notice_owner + "','" + hashlib.sha256(notice_id.encode()).hexdigest() + "',UTC_TIMESTAMP(6),CONVERT(0x" + notice_hex + " USING utf8mb4))")
        document = b.request('/api/documents/import', {'title': 'Redis 消费恢复笔记', 'text': 'Redis Streams PEL 消息通过 XAUTOCLAIM 恢复，持久化后确认。COHESION_FULL_NOTE'})
        original = parent(a)
        preview = review(a, original)
        assert preview['evidence_kind'] == 'fixture-no-model' and get(fixture_url + '/fixture/status')['calls'] == 0
        path = '/api/agent/executions/' + original['id'] + '/narration'
        invalid = {'request_key': uuid.uuid4().hex, 'review_hash': preview['review_hash'], 'confirm': False}
        a.request(path, invalid, status=400)
        a.request(path, dict(invalid, confirm=True, model_config={'url': 'http://evil.invalid'}), status=400)
        a.request(path, dict(invalid, confirm=True), origin='https://evil.invalid', status=403)
        Client().request('/api/agent/executions/' + original['id'], status=401)
        assert get(fixture_url + '/fixture/status')['calls'] == 0
        checks.append('review-and-consent-no-provider-side-effect; strict-fields; cookie-CSRF; authentication')
        child, receipt = confirm(a, original, preview)
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            duplicates = list(pool.map(lambda _: a.request(path, receipt), range(4)))
        assert all((v['id'] == child['id'] for v in duplicates))
        event_id, streamed = partial(a, child['id'])
        assert streamed['narration']['draft'] and streamed['state'] == 'RUNNING'
        response = a.request('/api/agent/executions/' + child['id'] + '/stream', headers={'Last-Event-ID': event_id}, stream=True)
        response.close()
        complete = wait(lambda: v if (v := task(a, child['id']))['state'] == 'COMPLETED' else None)
        assert '<img' in complete['answer'] and get(fixture_url + '/fixture/status')['calls'] == 1
        for endpoint, body in [('/api/agent/executions/' + child['id'], None), ('/api/agent/executions/' + child['id'] + '/stream', None), ('/api/agent/executions/' + child['id'] + '/cancel', {}), ('/api/agent/executions/' + child['id'] + '/narration/resume', receipt), (path, receipt), (path + '/preview', {'mask_name': ''})]:
            b.request(endpoint, body, status=404)
        checks.append('partial-before-EOF; observer-reconnect-read-only; concurrent-nonce-dedup; owner-isolation')
        canceled, _ = confirm(a, original, preview)
        partial(a, canceled['id'])
        a.request('/api/agent/executions/' + canceled['id'] + '/cancel', {})
        wait(lambda: get(fixture_url + '/fixture/status')['canceled'] >= 1)
        assert task(a, canceled['id'])['state'] == 'CANCELLED'
        checks.append('user-cancel-propagates-through-' + 'api' + '-to-upstream-HTTP')
        interrupted, initial_receipt = confirm(a, original, preview)
        partial(a, interrupted['id'])
        api.kill()
        api.wait(timeout=10)
        wait(lambda: get(fixture_url + '/fixture/status')['canceled'] >= 2)
        sql("UPDATE ct_m4.agent_executions SET lease_until=DATE_SUB(UTC_TIMESTAMP(6), INTERVAL 1 SECOND) WHERE id='" + interrupted['id'] + "'")
        before = get(fixture_url + '/fixture/status')['calls']
        api = api_start('api-restarted')
        recovered = task(a, interrupted['id'])
        assert recovered['can_resume'] and recovered['narration']['draft']
        a.request(path, initial_receipt)
        response = a.request('/api/agent/executions/' + interrupted['id'] + '/stream', stream=True)
        response.read()
        response.close()
        assert get(fixture_url + '/fixture/status')['calls'] == before
        retry = dict(initial_receipt, request_key=uuid.uuid4().hex)
        resume_path = '/api/agent/executions/' + interrupted['id'] + '/narration/resume'
        a.request(resume_path, dict(retry, confirm=False), status=400)
        a.request(resume_path, retry, status=409)
        resumed = a.request(resume_path, dict(retry, retry_unknown=True))
        assert resumed['resumes'] == 2
        final = wait(lambda: v if (v := task(a, interrupted['id']))['state'] == 'COMPLETED' else None)
        a.request(resume_path, initial_receipt)
        a.request(resume_path, dict(retry, retry_unknown=True))
        assert get(fixture_url + '/fixture/status')['calls'] == before + 1
        checks.append('SIGKILL-checkpoint-recovery; injected-expired-lease; no-auto-replay; unknown-outcome-explicit-retry; attempt-receipts')
        if args.playwright_module:
            browser_parent = parent(b)
            browser_paused, _ = confirm(b, browser_parent, review(b, browser_parent))
            partial(b, browser_paused['id'])
            api.send_signal(signal.SIGTERM)
            api.wait(timeout=15)
            api = api_start('api-browser-recovery')
            assert task(b, browser_paused['id'])['state'] == 'PAUSED'
            browser_env = dict(env, M4_BROWSER_URL=base, M4_BROWSER_EMAIL=credentials_b['email'], M4_BROWSER_PASSWORD=password, M4_BROWSER_FIXTURE=fixture_url, M4_PLAYWRIGHT_MODULE=args.playwright_module, M4_BROWSER_OUTPUT=str(work), M4_CHROMIUM=args.chromium or '', M4_BROWSER_PAUSED_ID=browser_paused['id'], M4_BROWSER_TRANSPORT='api', M4_BROWSER_APPLICATION_ID=application['id'], M4_BROWSER_INTERVIEW_ID=interview['id'], M4_BROWSER_DOCUMENT_ID=document['id'])
            result = subprocess.run(['node', str(repo / 'scripts/agent-explanation-browser.cjs')], env=browser_env, capture_output=True, text=True, timeout=120)
            (work / 'browser.stdout').write_text(result.stdout)
            (work / 'browser.stderr').write_text(result.stderr)
            if result.returncode:
                raise RuntimeError('browser validation failed: ' + result.stderr)
            checks.append('real-Chromium-consent-stream-refresh-XSS-and-cancel')

        def drained():
            return get(fixture_url + '/fixture/status')['active'] == 0
        wait(drained)
        artifacts = [('fixture-final', fixture_url + '/fixture/status')]
        assert get(fixture_url + '/fixture/status')['invalid_requests'] == 0
        checks.append('default-build-without-stream-SDK; authenticated-text-only-API-contract; no-vLLM-extensions')
        for name, url in artifacts:
            (work / (name + '.json')).write_text(json.dumps(get(url), indent=2))
        result = {'status': 'PASS', 'backend': 'fixture-no-model', 'transport': 'api', 'checks': checks, 'real_cloud_api': 'NOT_VALIDATED', 'real_vllm': 'NOT_VALIDATED', 'browser': 'PASS' if args.playwright_module else 'NOT_RUN', 'limits': ['No vLLM/model/GPU; no inference/cache/quality gains measured.', 'Lease expiration injected in disposable SQL after SIGKILL; not a 60-second elapsed-time measurement.']}
        (work / 'result.json').write_text(json.dumps(result, indent=2))
        print(json.dumps(result))
        print('Evidence: ' + str(work))
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        for handle in handles:
            handle.close()
        for name in reversed(containers):
            subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
if __name__ == '__main__':
    main()
