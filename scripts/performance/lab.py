#!/usr/bin/env python3
"""Owned, disposable HTTP performance lab. No .env or existing data volumes."""
import argparse
import concurrent.futures
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

from report import PHASES, compare, metric, validate

ROOT = Path(__file__).resolve().parents[2]
K6_IMAGE = 'grafana/k6:1.8.2'
LIMITS = {
    'api': (1, '512m'), 'worker': (1, '512m'),
    'analysis-1': (.5, '256m'), 'analysis-2': (.5, '256m'),
    'search': (1, '256m'), 'mysql': (1, '512m'),
    'redis': (.5, '128m'), 'etcd': (.5, '256m'),
    'opensearch': (1, '1536m'),
}
RUNTIME_ROOTS = ('cmd/', 'internal/', 'web/', 'migrations/')
RUNTIME_FILES = ('go.mod', 'go.sum', 'Dockerfile', 'Dockerfile.search', '.dockerignore', 'docker-compose.yml', 'compose.app.yml', 'compose.search.yml')


def run(command, *, cwd=ROOT, stdout=None, env=None, timeout=300):
    return subprocess.run(command, cwd=cwd, stdout=stdout, stderr=subprocess.STDOUT if stdout else None,
                          env=env, timeout=timeout, check=True, text=True)


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def snapshot(destination, revision):
    if revision:
        sha = subprocess.check_output(['git', 'rev-parse', '--verify', '--end-of-options', revision + '^{commit}'], cwd=ROOT, text=True).strip()
        archive = destination.parent / 'source.tar'
        with archive.open('wb') as output:
            run(['git', 'archive', sha], stdout=output)
        with tarfile.open(archive) as source:
            source.extractall(destination, filter='data')
        archive.unlink()
        files = [p for p in destination.rglob('*') if p.is_file() and (p.relative_to(destination).as_posix().startswith(RUNTIME_ROOTS) or p.name in RUNTIME_FILES)]
    else:
        sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
        names = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode().split('\0')
        files = []
        for name in sorted(set(names)):
            if not name or not (name in RUNTIME_FILES or name.startswith(RUNTIME_ROOTS)):
                continue
            original = ROOT / name
            if not original.is_file():
                continue
            if not original.resolve().is_relative_to(ROOT):
                raise ValueError('Build source must stay inside this repository')
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(original, target)
            files.append(target)
    if not (destination / 'Dockerfile.search').is_file():
        raise ValueError('The selected revision does not contain the Search service')
    hashes = {p.relative_to(destination).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    return {'revision': sha, 'selection': revision or 'worktree', 'runtime_sha256': digest(hashes)}


class Lab:
    def __init__(self, args):
        self.args = args
        self.id = uuid.uuid4().hex[:12]
        self.project = 'ct-perf-' + self.id
        self.evidence = args.output.resolve()
        if self.evidence.exists() and any(self.evidence.iterdir()):
            raise ValueError('Choose a new empty output directory; previous evidence is never overwritten')
        self.evidence.mkdir(parents=True, exist_ok=True)
        self.temporary = Path(tempfile.mkdtemp(prefix='campustrace-perf-'))
        self.compose_file = self.temporary / 'compose.json'
        self.compose = ['docker', 'compose', '--project-name', self.project, '--file', str(self.compose_file)]
        self.containers = []
        self.urls = {}
        self.environment = os.environ.copy()
        self.environment.update(SEARCH_QUERY_TOKEN='synthetic-perf-query-token-at-least-32', SEARCH_WRITE_TOKEN='synthetic-perf-write-token-at-least-32', LLM_URL='', LLM_API_KEY='', LLM_MODEL='', JWT_SECRET='synthetic-perf-jwt-secret-at-least-32',
                                OTEL_TRACES_EXPORTER='none', OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='',
                                CAMPUS_DATABASE='campustrace_perf_' + self.id, CAMPUS_REDIS_PREFIX='perf:' + self.id + ':')
        self.report = {'schema_version':1,'kind':'isolated-synthetic-http-performance','run_id':self.id,
                       'workload':{'jobs':args.jobs,'seconds':args.seconds,'query_rate':args.rate,'write_rate':args.write_rate,
                                   'reader_vus':args.max_vus,'query_limit':10,'read_accounts':2,'jd_template_version':1,'seed_analysis':'suppressed_in_owned_fixture'},
                       'phases':{},'external_model_calls':0,
                       'limits':'Single-host synthetic HTTP test; not production capacity, HA, model quality or user-effect evidence.'}

    def compose_run(self, *args, timeout=300, log=None):
        if log:
            with (self.evidence / log).open('w') as output:
                return run(self.compose + list(args), stdout=output, env=self.environment, timeout=timeout)
        return run(self.compose + list(args), env=self.environment, timeout=timeout)

    def request(self, service, path, body=None, token=''):
        headers = {'Accept':'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if body is not None:
            headers['Content-Type'] = 'application/json'
        request = urllib.request.Request(self.urls[service] + path, headers=headers,
                                         data=None if body is None else json.dumps(body).encode())
        with urllib.request.urlopen(request, timeout=8) as response:
            raw = response.read(2 << 20)
        return json.loads(raw) if raw.startswith((b'{', b'[')) else raw.decode()

    def wait(self, label, check, seconds=180):
        deadline = time.monotonic() + seconds
        last = ''
        while time.monotonic() < deadline:
            try:
                result = check()
                if result:
                    return result
            except (urllib.error.URLError, TimeoutError, ValueError, KeyError, AssertionError) as error:
                last = type(error).__name__
            time.sleep(.3)
        raise RuntimeError(label + ' did not converge: ' + last)

    def boot(self):
        source = self.temporary / 'source'
        source.mkdir()
        self.report['source'] = snapshot(source, self.args.revision)
        self.image = 'campustrace-perf:' + self.id
        self.search_image = 'campustrace-perf:' + self.id + '-search'
        if subprocess.run(['docker','image','inspect',K6_IMAGE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode:
            with (self.evidence/'k6-image.log').open('w') as output:
                run(['docker','pull',K6_IMAGE],stdout=output,timeout=300)
        print('Building a frozen source snapshot', flush=True)
        for dockerfile, image, logfile in [('Dockerfile',self.image,'build.log'),('Dockerfile.search',self.search_image,'search-build.log')]:
            with (self.evidence / logfile).open('w') as output:
                run(['docker','build','--file',dockerfile,'--tag',image,'.'], cwd=source, stdout=output, timeout=600)
        command = ['docker','compose','--env-file','/dev/null','--file','docker-compose.yml','--file','compose.app.yml','--file','compose.search.yml','config','--format','json']
        config = json.loads(subprocess.check_output(command, cwd=source, env=self.environment, text=True))
        config['name'] = self.project
        config['services'].pop('sandbox-runner', None)
        for name, service in config['services'].items():
            service.pop('profiles', None)
            service.pop('ports', None)
            service.pop('build', None)
            service['restart'] = 'no'
            service['cpus'], service['mem_limit'] = LIMITS[name]
            if name in ('api','worker','analysis-1','analysis-2'):
                service['image'] = self.image
            if name == 'search':
                service['image'] = self.search_image
            settings = service.get('environment', {})
            for key in list(settings):
                if key.startswith(('LLM_', 'INFERENCE_', 'OTEL_')):
                    settings[key] = ''
            settings['OTEL_TRACES_EXPORTER'] = 'none'
            if name == 'mysql':
                settings['MYSQL_DATABASE'] = self.environment['CAMPUS_DATABASE']
                service['volumes'] = []
                service['tmpfs'] = ['/var/lib/mysql']
            if name == 'redis':
                service['volumes'] = []
                service['command'] = ['redis-server','--appendonly','no','--save','']
            if name == 'etcd':
                service['volumes'] = []
                service['tmpfs'] = ['/etcd-data']
            if name == 'opensearch':
                service['volumes'] = []
                service['tmpfs'] = ['/usr/share/opensearch/data:uid=1000,gid=1000']
            if name in ('api','worker','search'):
                port = {'api':8080,'worker':18081,'search':18083}[name]
                service['ports'] = [{'target':port,'published':'0','host_ip':'127.0.0.1','protocol':'tcp'}]
        # Private stores stay internal. A separate control bridge publishes only
        # API/metrics to loopback; model providers remain explicitly disabled.
        config.setdefault('networks', {}).setdefault('default', {})['internal'] = True
        config['networks']['perf-control'] = {'internal':False}
        for name in ('api','worker','search'):
            config['services'][name].setdefault('networks', {})['perf-control'] = None
        for network in config.get('networks', {}).values():
            network.pop('name', None)
        for volume in config.get('volumes', {}).values():
            volume.pop('name', None)
        self.compose_file.write_text(json.dumps(config))
        print('Starting exclusively owned containers', flush=True)
        self.compose_run('up','-d','--wait','--wait-timeout','180', timeout=220, log='startup.log')
        for service, port in [('api',8080),('worker',18081),('search',18083)]:
            self.refresh_address(service,port)
        image_info = json.loads(subprocess.check_output(['docker','image','inspect',K6_IMAGE], text=True))[0]
        info = json.loads(subprocess.check_output(['docker','info','--format','{{json .}}'], text=True))
        self.report['environment'] = {'host_arch':platform.machine(),'host_identity_hash':digest(platform.node()),
                                      'docker_arch':info['Architecture'],'docker_cpus':info['NCPU'],
                                      'docker_memory':info['MemTotal'],'docker_version':info['ServerVersion'],
                                      'container_limits':json.loads(json.dumps(LIMITS)),'k6_image':K6_IMAGE,'k6_image_id':image_info['Id'],
                                      'tracing':'disabled','transport':'HTTP inside Docker network','lab_topology_version':1}

    def refresh_address(self,service,port):
        address=subprocess.check_output(self.compose+['port',service,str(port)],text=True).strip()
        if not re.fullmatch(r'127\.0\.0\.1:[1-9]\d*',address):
            raise ValueError(f'Owned loopback port missing: {service} {address!r}')
        self.urls[service]='http://'+address

    def seed(self):
        self.compose_run('stop','-t','10','worker',log='seed-pause-worker.log')
        self.users = []
        for i in range(2):
            account = {'email':f'perf-{self.id}-{i}@synthetic.invalid','password':'Synthetic2026!'}
            self.request('api','/auth/register',account)
            login = self.request('api','/auth/login',account)
            token = login['token']
            cap = self.request('api','/api/profile/resume/capabilities',token=token)
            self.users.append({'id':cap['user_id'],'token':token,'topics':['loadterm'+str(n) for n in ([1,2,3,5,6,7] if i == 0 else [0,4])]})
        self.company = 'Synthetic k6 ' + self.id
        self.jd = ('任职要求：熟悉 Golang、Redis、MySQL、分布式系统与网络编程。工作职责：开发可恢复的后端服务。 ' * 8)
        def ingest(i):
            user = self.users[1 if i % 4 == 0 else 0]
            body = {'company':self.company,'title':f'合成后端岗位 {i:05d}','job_type':'FULL_TIME','locations':['上海'],
                    'external_id':f'perf-{self.id}-{i:05d}','fetch_status':'SUCCESS','text':self.jd + f'loadterm{i % 8} synthetic{i:05d}'}
            return self.request('api','/api/ingest',body,user['token'])
        print(f'Seeding {self.args.jobs} synthetic jobs through the real API', flush=True)
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
            for i, _ in enumerate(executor.map(ingest, range(self.args.jobs)), 1):
                if i % 250 == 0:
                    print(f'Seeded {i}/{self.args.jobs}', flush=True)
        self.writer_input = {'company':self.company,'title':'合成并发更新岗位','job_type':'FULL_TIME','locations':['上海'],
                             'external_id':'perf-writer-' + self.id,'fetch_status':'SUCCESS','text':self.jd + 'loadterm1'}
        observation = self.request('api','/api/ingest',self.writer_input,self.users[0]['token'])
        self.writer_id = observation['job_id']
        self.fixture = {'schema_version':1,'run_id':self.id,'base_url':'http://api:8080','company':self.company,'users':self.users,
                        'writer':{'job_id':self.writer_id,'input':self.writer_input}}
        private = self.temporary / 'input'
        private.mkdir(mode=0o700)
        (private / 'fixture.json').write_text(json.dumps(self.fixture))
        (private / 'fixture.json').chmod(0o600)
        self.report['workload']['jd_utf8_bytes'] = len(self.jd.encode())
        self.report['seeded_jobs'] = self.args.jobs + 1
        scripts = self.temporary / 'load-script'
        scripts.mkdir()
        load_hashes = {}
        for name in ('search.js','contract.mjs'):
            raw = (ROOT/'scripts/performance'/name).read_bytes()
            (scripts/name).write_bytes(raw)
            load_hashes[name] = hashlib.sha256(raw).hexdigest()
        self.report['workload']['load_script_sha256'] = digest(load_hashes)
        # Search load does not measure initial extraction. Keep its transactional
        # search journal, but suppress only this synthetic fixture's initial
        # ANALYZE/ASSESS tasks before any consumer can receive them. Later writes
        # use the full production pipeline unchanged.
        rule_file = (self.temporary/'source/internal/domain/model.go').read_text()
        version = re.search(r'const RuleVersion = "([^"]+)"',rule_file)
        if not version:
            raise ValueError('Cannot identify fixture rule version')
        query = """START TRANSACTION;
          SELECT COUNT(*) FROM outbox WHERE sent=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type')) IN ('ANALYZE','ASSESS');
          DELETE FROM outbox WHERE sent=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type')) IN ('ANALYZE','ASSESS');
          UPDATE job_assessment_schedule SET next_assess_at=NULL,pending_task_id=NULL,pending_until=NULL,rule_version=UNHEX('%s');
          COMMIT;""" % version.group(1).encode().hex()
        if self.environment['CAMPUS_DATABASE'] != 'campustrace_perf_' + self.id:
            raise ValueError('Fixture preparation requires the exclusively owned schema')
        result = subprocess.run(self.compose+['exec','-T','mysql','sh','-c',
          'MYSQL_PWD="$MYSQL_PASSWORD" mysql --batch --skip-column-names -u"$MYSQL_USER" "$MYSQL_DATABASE"'],
          input=query,text=True,capture_output=True,check=True)
        self.report['preparation_analysis_tasks_suppressed'] = int(result.stdout.strip())
        self.compose_run('start','worker',log='seed-resume-worker.log')
        self.refresh_address('worker',18081)
        print('Waiting for the search projection to settle (initial extraction excluded)', flush=True)
        self.wait('background queue drain', self.drained, max(180, self.args.jobs / 10))
        self.wait('initial search catchup', self.fulltext_ready, max(180, self.args.jobs / 20))

    def query(self, user, topic):
        return self.request('api','/api/jobs/search',{'text':topic,'company':self.company,'limit':10},user['token'])

    def fulltext_ready(self):
        for user in self.users:
            result=self.query(user,user['topics'][0])
            if result['mode']!='fulltext' or not result['jobs']:
                return False
            if any(job['owner_id']!=user['id'] or job['company']!=self.company for job in result['jobs']):
                raise ValueError('Recovery response did not preserve fixture ownership')
        return True

    def drained(self):
        queues = self.request('worker','/queues')
        if any(row['outstanding'] or row['ready'] or row['inflight'] for row in queues):
            return False
        output = subprocess.check_output(self.compose + ['exec','-T','mysql','sh','-c',
            'MYSQL_PWD="$MYSQL_PASSWORD" mysql --batch --skip-column-names -u"$MYSQL_USER" "$MYSQL_DATABASE" -e "SELECT COUNT(*) FROM outbox WHERE sent=FALSE"'],text=True)
        return output.strip() == '0'

    def capture(self, phase, point):
        result = {}
        for service in ('api','worker','search'):
            try:
                text = self.request(service,'/metrics/prometheus')
                (self.evidence / f'{phase}-{point}-{service}.prom').write_text(text)
                selected = {}
                for line in text.splitlines():
                    fields = line.split()
                    if len(fields) == 2 and fields[0] in ('campustrace_db_wait_total','campustrace_db_wait_seconds_total',
                        'campustrace_db_connections_open','campustrace_db_connections_in_use','campustrace_search_pending',
                        'campustrace_search_ready','campustrace_search_fulltext_total','campustrace_search_basic_total'):
                        selected[fields[0]] = float(fields[1])
                result[service] = selected
            except (urllib.error.URLError, TimeoutError):
                result[service] = {'scrape_failed':True}
        ids = subprocess.check_output(self.compose + ['ps','--quiet'],text=True).split()
        if ids:
            raw = subprocess.check_output(['docker','stats','--no-stream','--format','{{json .}}',*ids],text=True)
            (self.evidence / f'{phase}-{point}-containers.jsonl').write_text(raw)
        return result

    def phase(self, name):
        print('Running phase: ' + name, flush=True)
        if name == 'fallback':
            self.compose_run('stop','-t','5','search', log='fallback-stop.log')
            if any(self.query(user,user['topics'][0])['mode'] != 'basic' for user in self.users):
                raise ValueError('Fallback preflight failed')
        else:
            self.wait(name + ' warmup', self.fulltext_ready)
        for _ in range(3):
            for user in self.users:
                self.query(user, user['topics'][0])
        before = self.capture(name,'before')
        output = self.evidence / name
        output.mkdir()
        container = self.project + '-k6-' + name
        self.containers.append(container)
        command = ['docker','run','--rm','--name',container,'--label','campustrace.perf.run=' + self.id,
                   '--network',self.project + '_default','--cpus','1','--memory','512m','--read-only','--tmpfs','/tmp',
                   '--user',f'{os.getuid()}:{os.getgid()}',
                   '--volume',str(self.temporary/'load-script') + ':/scripts:ro',
                   '--volume',str(self.temporary/'input') + ':/input:ro','--volume',str(output) + ':/results:rw']
        for key, value in {'PERF_CASE':name,'PERF_RUN_ID':self.id,'PERF_SECONDS':self.args.seconds,'PERF_RATE':self.args.rate,
                           'PERF_WRITE_RATE':self.args.write_rate,'PERF_MAX_VUS':self.args.max_vus,'K6_NO_USAGE_REPORT':'true'}.items():
            command += ['--env',key + '=' + str(value)]
        command += [K6_IMAGE,'run','--no-color','/scripts/search.js']
        started = threading.Event()
        with (output/'k6.log').open('w') as logfile:
            process = subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
            def copy_log():
                for line in process.stdout:
                    logfile.write(line)
                    if 'PERF_LOAD_STARTED' in line:
                        started.set()
            reader = threading.Thread(target=copy_log,daemon=True)
            reader.start()
            injected = False
            rebuild_source = None
            rebuild_plan = None
            try:
                if not started.wait(20):
                    raise RuntimeError('Load script did not start; inspect k6.log')
                if name == 'rebuild':
                    time.sleep(2)
                    rebuild_source = self.request('search','/v1/status',token=self.environment['SEARCH_QUERY_TOKEN'])
                    self.compose_run('exec','-T','search','/app/search','rebuild',timeout=15,log='rebuild-inject.log')
                    rebuild_status = self.request('search','/v1/status',token=self.environment['SEARCH_QUERY_TOKEN'])
                    rebuild_plan = rebuild_status.get('rebuild')
                    injected = True
                code = process.wait(timeout=self.args.seconds + 40)
            finally:
                if process.poll() is None:
                    subprocess.run(['docker','rm','--force',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                    process.wait(timeout=15)
                reader.join(timeout=5)
        summary = json.loads((output/'summary.json').read_text())
        summary.update(exit_code=code,rebuild_injected=injected,metrics_before=before)
        self.report['phases'][name] = summary
        if name == 'rebuild':
            summary['rebuild_mode'] = 'online' if rebuild_plan else 'replace'
            if rebuild_plan:
                summary['rebuild_source_generation'] = rebuild_source['generation']
                summary['rebuild_target_generation'] = rebuild_plan['target_generation']
                def rebuilt():
                    status = self.request('search','/v1/status',token=self.environment['SEARCH_QUERY_TOKEN'])
                    plan = status.get('rebuild',{})
                    return (plan.get('operation') == rebuild_plan['operation'] and plan.get('phase') == 'DONE'
                            and status['generation'] == rebuild_plan['target_generation'])
                self.wait('online rebuild reaches acknowledged cutover',rebuilt,max(90,self.args.jobs / 20))
                summary['rebuild_completed'] = True
        recovery_start = time.monotonic()
        if name == 'fallback':
            self.compose_run('start','search',log='fallback-restart.log')
            # Docker may assign a new ephemeral port when a container restarts.
            self.refresh_address('search',18083)
        self.wait(name + ' recovery', self.fulltext_ready, max(90,self.args.jobs / 20))
        summary['recovered_fulltext'] = True
        summary['recovery_wait_seconds_after_load'] = time.monotonic() - recovery_start
        if name == 'mixed':
            self.wait('mixed background drain', self.drained)
            detail = self.request('api','/api/jobs/' + self.writer_id,token=self.users[0]['token'])
            latest = max(detail['observations'],key=lambda item:(item['observed_at'],item['id']))
            marker = re.search(r'perfwrite\d{8}',latest['text'])
            if not marker:
                raise ValueError('No concurrent update reached the authoritative latest observation')
            def visible():
                value = self.query(self.users[0],marker.group())
                return value['mode']=='fulltext' and value['job_ids']==[self.writer_id]
            self.wait('latest concurrent update indexed',visible)
            summary['latest_write_visible'] = True
        summary['metrics_after'] = self.capture(name,'after')
        if code:
            raise RuntimeError(name + ' load checks failed; inspect its summary and log')

    def cleanup(self):
        for container in self.containers:
            subprocess.run(['docker','rm','--force',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        if self.compose_file.exists():
            try:
                self.compose_run('logs','--no-color','--tail','500',log='services.log',timeout=30)
            except (subprocess.SubprocessError, OSError):
                pass
            subprocess.run(self.compose+['down','--volumes','--remove-orphans'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=60)
        for image in (getattr(self,'image',''),getattr(self,'search_image','')):
            if image:
                subprocess.run(['docker','image','rm',image],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        shutil.rmtree(self.temporary)

    def execute(self):
        try:
            self.boot()
            self.seed()
            for name in PHASES:
                self.phase(name)
            errors = validate(self.report)
            comparisons = []
            if self.args.baseline:
                errors, comparisons = compare(self.report,json.loads(self.args.baseline.read_text()),self.args.ratio,self.args.allowance_ms)
            self.report['validation'] = {'passed':not errors,'errors':errors}
            self.report['comparison'] = {'performed':bool(self.args.baseline),'ratio':self.args.ratio,'allowance_ms':self.args.allowance_ms,'quantiles':comparisons}
            self.report['status'] = 'failed' if errors else 'passed' if self.args.baseline else 'baseline_recorded'
            if errors:
                raise ValueError('; '.join(errors))
            print('All four HTTP phases passed; ' + self.report['status'],flush=True)
        except BaseException as error:
            self.report['status'] = 'failed'
            self.report['failure'] = str(error)
            raise
        finally:
            (self.evidence/'report.json').write_text(json.dumps(self.report,indent=2,ensure_ascii=False)+'\n')
            self.cleanup()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--jobs',type=int,default=1000)
    parser.add_argument('--seconds',type=int,default=20)
    parser.add_argument('--rate',type=int,default=10)
    parser.add_argument('--write-rate',type=int,default=1)
    parser.add_argument('--max-vus',type=int,default=32)
    parser.add_argument('--output',type=Path,default=ROOT/'bin/verification/performance'/time.strftime('%Y%m%d-%H%M%S'))
    parser.add_argument('--revision',help='Test a committed revision instead of the current runtime source snapshot')
    parser.add_argument('--baseline',type=Path,help='Compare a previous same-environment report; no cross-machine comparison')
    parser.add_argument('--ratio',type=float,default=1.5)
    parser.add_argument('--allowance-ms',type=float,default=25)
    args=parser.parse_args()
    if not (100<=args.jobs<=10000 and 10<=args.seconds<=120 and 1<=args.rate<=100 and 1<=args.write_rate<=5 and 4<=args.max_vus<=64):
        parser.error('Workload bounds: jobs 100..10000, seconds 10..120, rate 1..100, writes 1..5, max VUs 4..64')
    if not (math.isfinite(args.ratio) and math.isfinite(args.allowance_ms) and args.ratio>=1 and 0<=args.allowance_ms<=10000):
        parser.error('Invalid regression policy')
    def interrupted(signum, frame):
        raise KeyboardInterrupt('Performance lab interrupted')
    for event in (signal.SIGTERM,signal.SIGHUP):
        signal.signal(event,interrupted)
    Lab(args).execute()


if __name__=='__main__':
    main()
