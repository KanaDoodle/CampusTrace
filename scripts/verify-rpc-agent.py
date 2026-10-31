#!/usr/bin/env python3
"""M4 acceptance: isolated SQL/Redis + real API/RPC/HTTP processes, NO MODEL.

API mode requires Go and Docker; RPC mode additionally uses the SDK candidate
or sibling KanaRPC-Go. Optional --playwright-module enables
real Chromium UI checks. All containers, processes and data belong to this run.
"""
import argparse
import concurrent.futures
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
    parser.add_argument('--backend', choices=['api', 'rpc'], default='rpc', help='direct daily API or optional RPC experiment; both use NO MODEL fixtures')
    parser.add_argument('--rpc-repo', type=pathlib.Path)
    parser.add_argument('--sdk-candidate', type=pathlib.Path, help='versioned SDK candidate; no sibling checkout/workspace required')
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--playwright-module', help='absolute installed playwright module path')
    parser.add_argument('--chromium', help='optional Chromium executable')
    args = parser.parse_args()
    if args.backend == 'api' and (args.sdk_candidate or args.rpc_repo):
        parser.error('API acceptance does not use an RPC checkout or SDK candidate')
    repo = pathlib.Path(__file__).resolve().parents[1]
    rpc = (args.rpc_repo or repo.parent / 'KanaRPC-Go').resolve()
    work = (args.output or pathlib.Path(tempfile.mkdtemp(prefix='campustrace-m4-'))).resolve()
    work.mkdir(parents=True, exist_ok=True)
    processes, containers, handles, checks = [], [], [], []
    run_id = uuid.uuid4().hex[:10]
    password = 'm4-synthetic-only'
    env = dict(os.environ, GOWORK='off', SEARCH_URL='', SEARCH_QUERY_TOKEN='', SEARCH_WRITE_TOKEN='',
               LLM_URL='', LLM_API_KEY='', LLM_MODEL='', OTEL_EXPORTER_OTLP_ENDPOINT='',
               OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='', OTEL_EXPORTER_OTLP_METRICS_ENDPOINT='',
               CAMPUS_LIVE_SOURCES='0', VLLM_API_KEY='', KANARPC_FIXTURE_KEY='',
               NARRATION_BACKEND='disabled', INFERENCE_RPC_ADDRESS='', INFERENCE_MODEL='',
               INFERENCE_BACKEND_KIND='', JOB_EVENTS_MODE='direct', KAFKA_BROKERS='')

    def run(command, **kwargs):
        return subprocess.run(command, check=True, text=True, capture_output=True, env=env, **kwargs).stdout.strip()

    def wait(check, seconds=15):
        until = time.monotonic() + seconds
        while time.monotonic() < until:
            value = check()
            if value:
                return value
            time.sleep(.05)
        raise RuntimeError('acceptance condition timed out')

    def launch(name, command, child_env=None):
        out, err = open(work / (name + '.jsonl'), 'w'), open(work / (name + '.stderr'), 'w')
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

        def request(self, path, body=None, status=200, origin=None, headers=None, stream=False):
            req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(),
                                         headers={'Content-Type': 'application/json', 'Origin': origin or base, **(headers or {})})
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
        value = wait(lambda: (v if (v := task(client, value['id']))['state'] == 'COMPLETED' else None))
        assert value['source_identity'] and value['grounded_observations']
        return value

    def review(client, value):
        return client.request('/api/agent/executions/' + value['id'] + '/narration/preview', {'mask_name': ''})

    def confirm(client, value, preview):
        body = {'request_key': uuid.uuid4().hex, 'review_hash': preview['review_hash'], 'confirm': True, 'mask_name': ''}
        return client.request('/api/agent/executions/' + value['id'] + '/narration', body), body

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
                        return event_id, value
        finally:
            response.close()
        raise AssertionError('no partial snapshot before terminal')

    try:
        if args.backend == 'api':
            run(['go', 'build', '-mod=readonly', '-o', str(work / 'api'), './cmd/api'], cwd=repo)
        elif args.sdk_candidate:
            from stream_sdk import prepare
            sdk_env, modfile, sdk_dir, resolution = prepare(repo, args.sdk_candidate.resolve(), work / 'sdk')
            env.update({key: sdk_env[key] for key in ('GOWORK', 'GOMODCACHE', 'GOPROXY', 'GONOSUMDB', 'GOPRIVATE', 'GONOPROXY')})
            (work / 'sdk-resolution.json').write_text(json.dumps(resolution, indent=2))
            for name in ['inference-fixture', 'inference-worker', 'inference-gateway']:
                run(['go', 'build', '-mod=readonly', '-o', str(work / name), './cmd/' + name], cwd=sdk_dir)
            run(['go', 'build', '-modfile=' + str(modfile), '-tags', 'kanarpc_stream', '-o', str(work / 'api'), './cmd/api'], cwd=repo)
        else:
            for name in ['inference-fixture', 'inference-worker', 'inference-gateway']:
                run(['go', 'build', '-o', str(work / name), './cmd/' + name], cwd=rpc)
            # Development mode. Packaged acceptance above always disables workspaces.
            run(['go', 'work', 'init', str(repo), str(rpc)], cwd=work)
            env['GOWORK'] = str(work / 'go.work')
            run(['go', 'build', '-tags', 'kanarpc_stream', '-o', str(work / 'api'), './cmd/api'], cwd=repo)
        mysql = container('mysql', 'mysql:8.4', [], ['--tmpfs', '/var/lib/mysql', '-p', '127.0.0.1::3306',
                          '-e', 'MYSQL_ROOT_PASSWORD=' + password, '-e', 'MYSQL_DATABASE=ct_m4',
                          '-e', 'MYSQL_USER=m4', '-e', 'MYSQL_PASSWORD=' + password])
        redis = container('redis', 'redis:7.4-alpine', ['redis-server', '--save', '', '--appendonly', 'no'],
                          ['-p', '127.0.0.1::6379'])

        def mysql_ready():
            result = subprocess.run(['docker', 'exec', mysql, 'mysql', '-h127.0.0.1', '-um4', '-p' + password,
                                     '-Dct_m4', '-e', 'SELECT 1'],
                                    capture_output=True, env=env)
            return result.returncode == 0
        wait(mysql_ready, 90)
        if args.backend == 'api':
            fixture = ready('fixture', launch('fixture', [sys.executable, str(repo / 'scripts/model-api-fixture.py')]))
            fixture_url = 'http://' + fixture['address']
        else:
            fixture = ready('fixture', launch('fixture', [str(work / 'inference-fixture'), '-listen', '127.0.0.1:0', '-mode', 'narration']))
            fixture_url = 'http://' + fixture['address']
            worker = ready('worker', launch('worker', [str(work / 'inference-worker'), '-listen', '127.0.0.1:0',
                           '-admin-listen', '127.0.0.1:0', '-upstream', fixture_url, '-model', 'fixture-model',
                           '-api-key-env', 'KANARPC_FIXTURE_KEY', '-profile', 'fixture-serial-v1',
                           '-tokenizer', 'fixture-words-plus-template-v1', '-backend-kind', 'fixture-no-model', '-concurrency', '4']))
            config = {'model': 'fixture-model', 'policy': 'token_work', 'refresh_interval_ns': 100000000,
                      'workers': [{'id': 'fixture-worker', 'address': worker['address'], 'admin_url': worker['admin_url'],
                                   'profile': {'model': 'fixture-model', 'id': 'fixture-serial-v1',
                                               'tokenizer_id': 'fixture-words-plus-template-v1',
                                               'prefill_tokens_per_second': 100000, 'decode_tokens_per_second': 100000,
                                               'base_ttft_ns': 1000000, 'max_in_flight': 4}}]}
            (work / 'gateway-config.json').write_text(json.dumps(config, indent=2))
            gateway = ready('gateway', launch('gateway', [str(work / 'inference-gateway'), '-config', str(work / 'gateway-config.json'),
                             '-listen', '127.0.0.1:0', '-admin-listen', '127.0.0.1:0']))
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            http_address = '127.0.0.1:' + str(sock.getsockname()[1])
        base = 'http://' + http_address
        api_env = dict(env, MYSQL_DSN='m4:' + password + '@tcp(127.0.0.1:' + mapped(mysql, 3306) + ')/ct_m4?parseTime=true&loc=UTC',
                       REDIS_ADDR='127.0.0.1:' + mapped(redis, 6379), JWT_SECRET='m4-synthetic-jwt-secret-only-32bytes',
                       HTTP_ADDR=http_address, NARRATION_BACKEND=args.backend,
                       INFERENCE_BACKEND_KIND='fixture-no-model', CAMPUS_REDIS_PREFIX='m4:' + run_id + ':')
        if args.backend == 'api':
            api_env.update(LLM_URL=fixture_url + '/v1/chat/completions', LLM_MODEL='fixture-model', LLM_API_KEY='synthetic-api-fixture-key')
        else:
            api_env.update(INFERENCE_RPC_ADDRESS=gateway['address'], INFERENCE_MODEL='fixture-model')

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
        a, b = Client(), Client()
        credentials_a, credentials_b = a.register('a'), b.register('b')
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
            # Cookie jar is read-only here; each submission carries the same logical nonce.
            duplicates = list(pool.map(lambda _: a.request(path, receipt), range(4)))
        assert all(v['id'] == child['id'] for v in duplicates)
        event_id, streamed = partial(a, child['id'])
        assert streamed['narration']['draft'] and streamed['state'] == 'RUNNING'
        # Disconnecting an observer and reattaching is read-only.
        response = a.request('/api/agent/executions/' + child['id'] + '/stream', headers={'Last-Event-ID': event_id}, stream=True)
        response.close()
        complete = wait(lambda: (v if (v := task(a, child['id']))['state'] == 'COMPLETED' else None))
        assert '<img' in complete['answer'] and get(fixture_url + '/fixture/status')['calls'] == 1
        for endpoint, body in [('/api/agent/executions/' + child['id'], None),
                               ('/api/agent/executions/' + child['id'] + '/stream', None),
                               ('/api/agent/executions/' + child['id'] + '/cancel', {}),
                               ('/api/agent/executions/' + child['id'] + '/narration/resume', receipt),
                               (path, receipt),
                               (path + '/preview', {'mask_name': ''})]:
            b.request(endpoint, body, status=404)
        checks.append('partial-before-EOF; observer-reconnect-read-only; concurrent-nonce-dedup; owner-isolation')
        canceled, _ = confirm(a, original, preview)
        partial(a, canceled['id'])
        a.request('/api/agent/executions/' + canceled['id'] + '/cancel', {})
        wait(lambda: get(fixture_url + '/fixture/status')['canceled'] >= 1)
        assert task(a, canceled['id'])['state'] == 'CANCELLED'
        checks.append('user-cancel-propagates-through-' + args.backend + '-to-upstream-HTTP')
        interrupted, initial_receipt = confirm(a, original, preview)
        partial(a, interrupted['id'])
        api.kill()
        api.wait(timeout=10)
        wait(lambda: get(fixture_url + '/fixture/status')['canceled'] >= 2)
        # Deliberate lease fault injection only in this disposable database.
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
        final = wait(lambda: (v if (v := task(a, interrupted['id']))['state'] == 'COMPLETED' else None))
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
            browser_env = dict(env, M4_BROWSER_URL=base, M4_BROWSER_EMAIL=credentials_b['email'], M4_BROWSER_PASSWORD=password,
                               M4_BROWSER_FIXTURE=fixture_url, M4_PLAYWRIGHT_MODULE=args.playwright_module,
                               M4_BROWSER_OUTPUT=str(work), M4_CHROMIUM=args.chromium or '',
                               M4_BROWSER_PAUSED_ID=browser_paused['id'], M4_BROWSER_TRANSPORT=args.backend)
            result = subprocess.run(['node', str(repo / 'scripts/rpc-agent-browser.cjs')], env=browser_env,
                                    capture_output=True, text=True, timeout=90)
            (work / 'browser.stdout').write_text(result.stdout)
            (work / 'browser.stderr').write_text(result.stderr)
            if result.returncode:
                raise RuntimeError('browser validation failed: ' + result.stderr)
            checks.append('real-Chromium-consent-stream-refresh-XSS-and-cancel')
        def drained():
            if args.backend == 'api':
                return get(fixture_url + '/fixture/status')['active'] == 0
            status = get(gateway['admin_url'] + '/status')
            return all(w['local_reservations'] == 0 for w in status['workers']) and not get(worker['admin_url'] + '/status')['active']
        wait(drained)
        artifacts = [('fixture-final', fixture_url + '/fixture/status')]
        if args.backend == 'rpc':
            artifacts += [('gateway-final', gateway['admin_url'] + '/status'), ('worker-final', worker['admin_url'] + '/status')]
        else:
            assert get(fixture_url + '/fixture/status')['invalid_requests'] == 0
            checks.append('default-build-without-stream-SDK; authenticated-text-only-API-contract; no-vLLM-extensions')
        for name, url in artifacts:
            (work / (name + '.json')).write_text(json.dumps(get(url), indent=2))
        result = {'status': 'PASS', 'backend': 'fixture-no-model', 'transport': args.backend, 'checks': checks,
                  'real_cloud_api': 'NOT_VALIDATED', 'real_vllm': 'NOT_VALIDATED',
                  'browser': 'PASS' if args.playwright_module else 'NOT_RUN',
                  'limits': ['No vLLM/model/GPU; no inference/cache/quality gains measured.',
                             'Lease expiration injected in disposable SQL after SIGKILL; not a 60-second elapsed-time measurement.']}
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
