#!/usr/bin/env python3
"""Run repeatable Go integration tests in owned SQL/Redis/etcd containers.

No .env, existing database or existing Compose project is used. Every Go test
scope creates its own restricted SQL account/schema; this script audits cleanup.
"""
import argparse
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid

sys.dont_write_bytecode = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--race', action='store_true')
    parser.add_argument('--count', type=int, default=2)
    parser.add_argument('--shuffle', default='60431')
    parser.add_argument('--run', help='optional Go test name expression')
    parser.add_argument('--sdk-candidate', type=pathlib.Path)
    parser.add_argument('--adversarial', action='store_true', help='also run gated process fixtures; no real model')
    args = parser.parse_args()
    if args.count < 1 or args.count > 10:
        raise SystemExit('count must be between 1 and 10')
    work = (args.output or pathlib.Path(tempfile.mkdtemp(prefix='ct-isolation-'))).resolve()
    if work.exists() and any(work.iterdir()):
        raise SystemExit('use a new empty output directory')
    work.mkdir(parents=True, exist_ok=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    env = dict(os.environ, GOWORK='off', CAMPUS_INTEGRATION='1', CAMPUS_LIVE_SOURCES='0',
               CAMPUS_ADVERSARIAL='1' if args.adversarial else '0', CAMPUS_ADVERSARIAL_EVIDENCE=str(work / 'processes.jsonl'),
               LLM_URL='', LLM_API_KEY='', LLM_MODEL='', SEARCH_URL='', SEARCH_QUERY_TOKEN='', SEARCH_WRITE_TOKEN='',
               NARRATION_BACKEND='api', INFERENCE_RPC_ADDRESS='', INFERENCE_MODEL='', INFERENCE_BACKEND_KIND='', JOB_EVENTS_MODE='direct', KAFKA_BROKERS='', OTEL_EXPORTER_OTLP_ENDPOINT='',
               OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='', OTEL_EXPORTER_OTLP_METRICS_ENDPOINT='')
    containers = []
    run_id = uuid.uuid4().hex[:12]
    password = 'local-root-only'

    def run(command):
        return subprocess.check_output(command, text=True, stderr=subprocess.PIPE).strip()

    def create(role, image, port, extra, command):
        name = 'ct-isolation-' + role + '-' + run_id
        run(['docker', 'run', '--detach', '--name', name, '--label', 'campustrace.verification=' + run_id,
             '--publish', '127.0.0.1::' + str(port), *extra, image, *command])
        containers.append(name)
        return name, run(['docker', 'port', name, str(port) + '/tcp']).split(':')[-1]

    def wait(condition):
        until = time.monotonic() + 90
        while time.monotonic() < until:
            if condition():
                return
            time.sleep(.1)
        raise RuntimeError('owned dependencies did not become ready')

    try:
        mysql, mysql_port = create('mysql', 'mysql:8.4', 3306, ['--tmpfs', '/var/lib/mysql', '-e', 'MYSQL_ROOT_PASSWORD=' + password,
                                  '-e', 'MYSQL_DATABASE=ct_template', '-e', 'MYSQL_USER=campus', '-e', 'MYSQL_PASSWORD=local-campus-only'], [])
        redis, redis_port = create('redis', 'redis:7.4-alpine', 6379, [], ['redis-server', '--save', '', '--appendonly', 'no'])
        etcd, etcd_port = create('etcd', 'quay.io/coreos/etcd:v3.6.7', 2379, [], ['etcd', '--data-dir=/tmp/test-etcd',
                                  '--listen-client-urls=http://0.0.0.0:2379', '--advertise-client-urls=http://127.0.0.1:2379'])
        wait(lambda: subprocess.run(['docker', 'exec', mysql, 'mysql', '-h127.0.0.1', '-ucampus', '-plocal-campus-only',
                                     '-Dct_template', '-e', 'SELECT 1'], capture_output=True).returncode == 0)

        def etcd_ready():
            try:
                with urllib.request.urlopen('http://127.0.0.1:' + etcd_port + '/version', timeout=1):
                    return True
            except OSError:
                return False
        wait(etcd_ready)

        def sql(query):
            return run(['docker', 'exec', mysql, 'mysql', '-uroot', '-p' + password, '-N', '-e', query])

        sql("CREATE TABLE ct_template.ownership_guard(id INT PRIMARY KEY, note VARCHAR(100)); INSERT INTO ct_template.ownership_guard VALUES(1,'preserve-template');")
        env.update(MYSQL_TEST_DSN='campus:local-campus-only@tcp(127.0.0.1:' + mysql_port + ')/ct_template?parseTime=true&loc=UTC',
                   MYSQL_TEST_ADMIN_DSN='root:' + password + '@tcp(127.0.0.1:' + mysql_port + ')/mysql?parseTime=true&loc=UTC',
                   MYSQL_TEST_CONTAINER=mysql, REDIS_ADDR='127.0.0.1:' + redis_port, ETCD_ENDPOINTS='127.0.0.1:' + etcd_port,
                   MYSQL_MIGRATION_TEST_DSN='', MYSQL_RADAR_MIGRATION_TEST_DSN='')
        env['MYSQL_DSN'] = env['MYSQL_TEST_DSN']
        command = ['go', 'test', '-count=' + str(args.count), '-shuffle=' + args.shuffle, '-timeout=900s', '-json']
        if args.race:
            command.append('-race')
        if args.sdk_candidate:
            from stream_sdk import prepare
            sdk_env, modfile, _, _ = prepare(repo, args.sdk_candidate.resolve(), work / 'sdk')
            for key in ('GOWORK', 'GOMODCACHE', 'GOPROXY', 'GONOSUMDB', 'GOPRIVATE', 'GONOPROXY'):
                env[key] = sdk_env[key]
            command += ['-tags=kanarpc_stream', '-modfile=' + str(modfile)]
        if args.run:
            command += ['-run', args.run]
        command.append('./...')
        with open(work / 'go-test.jsonl', 'w') as out, open(work / 'go-test.stderr', 'w') as err:
            result = subprocess.run(command, cwd=repo, env=env, stdout=out, stderr=err)
        schema_count = int(sql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name REGEXP '^(ct_test_|ct_recovery_fixture_|campustrace_restore_)'"))
        account_count = int(sql("SELECT COUNT(*) FROM mysql.user WHERE user REGEXP '^(ct_test_|ct_restore_)'"))
        marker = sql('SELECT note FROM ct_template.ownership_guard WHERE id=1')
        table_count = int(sql("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='ct_template'"))
        report = {'status': 'PASS' if result.returncode == 0 and schema_count == account_count == 0 and marker == 'preserve-template' and table_count == 1 else 'FAIL',
                  'go_exit': result.returncode, 'race': args.race, 'count': args.count, 'shuffle': args.shuffle,
                  'remaining_fixture_schemas': schema_count, 'remaining_fixture_accounts': account_count,
                  'template_guard_preserved': marker == 'preserve-template' and table_count == 1,
                  'backend': 'local-test-fixtures-no-model', 'real_vllm': 'NOT_VALIDATED'}
        (work / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
        print(json.dumps(report))
        if report['status'] != 'PASS':
            raise SystemExit(1)
    finally:
        for name in reversed(containers):
            subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
