#!/usr/bin/env python3
"""Serialized paired search diagnosis. Only disposable lab data; no .env.

A frozen baseline is required. --candidate runs A/B/B/A on each identical seeded
DB/index (two repetitions). Profiles/traces run separately from timing runs.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
import platform
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import time
import urllib.request

import lab

TOKEN = 'synthetic-profile-token-at-least-32-bytes'


def frozen(source, destination):
    if not source.is_dir() or not (source/'Dockerfile.search').is_file():
        raise ValueError('A frozen source directory is required')
    shutil.copytree(source, destination, dirs_exist_ok=True)
    hashes = {p.relative_to(source).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
              for p in source.rglob('*') if p.is_file() and (p.relative_to(source).as_posix().startswith(lab.RUNTIME_ROOTS) or p.name in lab.RUNTIME_FILES)}
    return {'selection':'frozen-directory', 'runtime_sha256':lab.digest(hashes)}


class Diagnosis(lab.Lab):
    def refresh_address(self, service, port):
        # Docker Desktop can report "invalid IP:0" just after Started. Never
        # connect until the inherited loopback-only address check succeeds.
        deadline=time.monotonic()+10
        while True:
            try:
                return super().refresh_address(service,port)
            except (ValueError,subprocess.CalledProcessError):
                if time.monotonic()>=deadline: raise
                time.sleep(.1)

    def wait(self, label, check, seconds=180):
        def checked():
            container=self.project+'-mysql-1'
            state=subprocess.check_output(['docker','inspect','--format','{{json .State}}',container],text=True)
            state=json.loads(state)
            if not state['Running']:
                (self.evidence/'mysql-failed-state.json').write_text(json.dumps(state,indent=2))
                raise RuntimeError('Owned MySQL stopped; OOM='+str(state['OOMKilled']))
            return check()
        return super().wait(label,checked,seconds)

    def prepare_source(self, destination):
        return frozen(self.args.source, destination)

    def configure(self, config):
        for name in ('api','search'):
            if platform.system() == 'Linux':
                config['services'][name]['extra_hosts'] = ['host.docker.internal:host-gateway']
            settings = config['services'][name]['environment']
            settings.update(PROFILE_ADDR='0.0.0.0:6060', PROFILE_TOKEN=TOKEN)
            config['services'][name]['ports'].append({'target':6060,'published':'0','host_ip':'127.0.0.1','protocol':'tcp'})

    def sql(self, text):
        if self.environment['CAMPUS_DATABASE'] != 'campustrace_perf_' + self.id:
            raise ValueError('SQL requires exclusively owned schema')
        return subprocess.run(self.compose+['exec','-T','mysql','sh','-c',
            'MYSQL_PWD="$MYSQL_PASSWORD" mysql --batch --raw --skip-column-names -u"$MYSQL_USER" "$MYSQL_DATABASE"'],
            input=text,text=True,capture_output=True,check=True).stdout

    def seed(self):
        self.sparse = [None, None]
        super().seed()
        if int(self.sql('SELECT COUNT(*) FROM jobs;').strip()) != self.args.jobs+1:
            raise ValueError('Exact scale count mismatch')
        rows = self.catalog_rows()
        sparse = self.sparse
        if not all(sparse): raise ValueError('Both sparse fixtures must come from API receipts')
        self.fixture['sparse'] = sparse
        (self.temporary/'input/fixture.json').write_text(json.dumps(self.fixture))
        for name in ('diagnose.js','contract.mjs','diagnose_contract.mjs'):
            shutil.copy2(lab.ROOT/'scripts/performance'/name, self.temporary/'load-script'/name)
        self.report['workload'].update(jobs=self.args.jobs+1, write_rate=0, mix={'common':.5,'sparse':.25,'miss':.25},
            load_script_sha256=lab.digest({n:hashlib.sha256((self.temporary/'load-script'/n).read_bytes()).hexdigest() for n in ('diagnose.js','contract.mjs','diagnose_contract.mjs')}))
        self.report['fixture_sha256'] = lab.digest(rows)
        # Store only synthetic identities; credentials remain in the temporary fixture.
        (self.evidence/'dataset.json').write_text(json.dumps({'jobs':self.args.jobs+1,'sha256':self.report['fixture_sha256'],'sparse':sparse},indent=2))
        # Freeze background maintenance in both variants after convergence.
        self.compose_run('stop','-t','10','worker',log='settled-worker-stop.log')
        self.report['generation_before'] = self.request('search','/v1/status',token=self.environment['SEARCH_QUERY_TOKEN'])['generation']

    def catalog_rows(self):
        return self.sql("SELECT j.id,j.owner_id,SHA2(CAST(j.body AS CHAR CHARACTER SET utf8mb4),256),o.id,SHA2(CAST(o.body AS CHAR CHARACTER SET utf8mb4),256) FROM jobs j JOIN observations o ON o.job_id=j.id ORDER BY j.id,o.id;").splitlines()

    def seeded_job(self, index, observation):
        owner = 1 if index % 4 == 0 else 0
        self.sparse[owner] = {'text':f'synthetic{index:05d}', 'id':observation['job_id']}

    def switch(self, variant, tracing=False):
        config = json.loads(self.compose_file.read_text())
        for service in ('api','search'):
            config['services'][service]['image'] = self.images[variant][0 if service=='api' else 1]
            settings = config['services'][service]['environment']
            settings['OTEL_TRACES_EXPORTER'] = '' if tracing else 'none'
            settings['OTEL_EXPORTER_OTLP_TRACES_ENDPOINT'] = f'http://host.docker.internal:{self.trace_port}/v1/traces' if tracing else ''
            settings['OTEL_TRACES_SAMPLER_ARG'] = '1'
            settings['INSTANCE_ID'] = self.id+'-'+getattr(self, 'trace_label', variant)
        self.compose_file.write_text(json.dumps(config))
        self.compose_run('up','-d','--no-deps','--force-recreate','api','search',log=f'{variant}-switch-{time.time_ns()}.log',timeout=100)
        for service, port in [('api',8080),('search',18083)]: self.refresh_address(service,port)
        self.wait('API and fulltext readiness', self.fulltext_ready)
        self.active_source = self.args.source if variant=='baseline' else self.args.candidate

    def mode(self, mode):
        if mode=='basic':
            self.compose_run('stop','-t','3','search',log=f'fallback-stop-{time.time_ns()}.log')
            self.wait('basic preflight',lambda: all(self.query(u,u['topics'][0])['mode']=='basic' for u in self.users))
        else:
            self.compose_run('start','search',log=f'search-start-{time.time_ns()}.log')
            self.refresh_address('search',18083)
            self.wait('fulltext preflight',self.fulltext_ready)
        # Identical, bounded application warmup, including both expensive cases.
        for _ in range(3):
            for i,u in enumerate(self.users):
                for text in (u['topics'][0],self.fixture['sparse'][i]['text'],'absentdiagnosticterm'):
                    try: self.query(u,text)
                    except Exception: pass  # Measurement retains the actual error denominator.

    def load(self, label, mode, seconds=None):
        output=self.evidence/label
        output.mkdir()
        container=self.project+'-load-'+label
        self.containers.append(container)
        command=['docker','run','--rm','--name',container,'--network',self.project+'_default',
            '--cpus','1','--memory','512m','--read-only','--tmpfs','/tmp','--user',f'{os.getuid()}:{os.getgid()}',
            '--volume',str(self.temporary/'load-script')+':/scripts:ro','--volume',str(self.temporary/'input')+':/input:ro',
            '--volume',str(output)+':/results:rw']
        for key,value in {'PERF_RUN_ID':self.id,'PERF_MODE':mode,'PERF_SECONDS':seconds or self.args.seconds,
                          'PERF_RATE':self.args.rate,'K6_NO_USAGE_REPORT':'true'}.items():
            command+=['--env',key+'='+str(value)]
        command += [lab.K6_IMAGE,'run','--no-color','/scripts/diagnose.js']
        print('Measuring '+label,flush=True)
        before=self.metrics()
        ids=subprocess.check_output(self.compose+['ps','--quiet','api','search','mysql'],text=True).split()
        with (output/'k6.log').open('w') as logfile, (output/'resources.jsonl').open('w') as resources:
            sampler=subprocess.Popen(['docker','stats','--format','{{json .}}',*ids],stdout=resources,stderr=subprocess.DEVNULL)
            try:
                result=subprocess.run(command,stdout=logfile,stderr=subprocess.STDOUT,timeout=(seconds or self.args.seconds)+40)
            finally:
                sampler.terminate()
                sampler.wait(timeout=5)
        summary=json.loads((output/'summary.json').read_text())
        summary.update(exit_code=result.returncode,metrics_before=before,metrics_after=self.metrics())
        (output/'summary.json').write_text(json.dumps(summary,indent=2))
        self.report['phases'][label]=summary
        self.save()
        return summary

    def metrics(self):
        text=self.request('api','/metrics/prometheus')
        selected={}
        for line in text.splitlines():
            fields=line.split()
            if len(fields)==2 and fields[0].startswith(('campustrace_db_','campustrace_search_','campustrace_go_','campustrace_process_')):
                try: selected[fields[0]]=float(fields[1])
                except ValueError: pass
        return selected

    def profile(self, service, name, seconds=0):
        address=subprocess.check_output(self.compose+['port',service,'6060'],text=True).strip()
        if not re.fullmatch(r'127\.0\.0\.1:\d+',address): raise ValueError('Private profiling port missing')
        url='http://'+address+'/debug/pprof/'+name+(f'?seconds={seconds}' if seconds else '')
        request=urllib.request.Request(url,headers={'Authorization':'Bearer '+TOKEN})
        with urllib.request.urlopen(request,timeout=seconds+10) as response: return response.read(32<<20)

    def explain(self, variant):
        source=(self.active_source/'internal/persistence/search_query.go').read_text()
        fallback=source[source.index('func (s *Store) basicSearch'):]
        queries=re.findall(r'QueryContext\([^`]*`([^`]+)`',fallback)
        if not queries: raise ValueError('Cannot identify the actual fallback SQL')
        owner=self.users[0]['id']
        query=queries[0].replace('?',"''",1).replace('?',"'"+owner+"'",1)
        if query.count('?') > 1: raise ValueError('Unexpected fallback SQL parameters')
        first=query.replace('?', '100')
        plans='FIRST PAGE (100)\n'+self.sql('EXPLAIN FORMAT=JSON '+first+';\nEXPLAIN ANALYZE '+first+';')
        if '?' in query:
            plans+='\nLATER BATCH (500; same range for plan inspection)\n'+self.sql('EXPLAIN ANALYZE '+query.replace('?', '500')+';')
        (self.evidence/(variant+'-explain.txt')).write_text(plans)
        (self.evidence/(variant+'-mysql-status.txt')).write_text(self.sql("SHOW INDEX FROM observations; SHOW INDEX FROM jobs; SHOW GLOBAL STATUS WHERE Variable_name IN ('Innodb_buffer_pool_reads','Innodb_buffer_pool_read_requests','Threads_running');"))

    def diagnostics(self, variant):
        self.switch(variant,tracing=True)
        self.explain(variant)
        for mode in ('fulltext','basic'):
            self.mode(mode)
            services=['api','search'] if mode=='fulltext' else ['api']
            for service in services:
                (self.evidence/f'{variant}-{mode}-{service}-allocs-before.pb.gz').write_bytes(self.profile(service,'allocs'))
            with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
                profiles={s:pool.submit(self.profile,s,'profile',10) for s in services}
                self.load(f'diagnostic-{variant}-{mode}',mode,12)
                for service, future in profiles.items():
                    (self.evidence/f'{variant}-{mode}-{service}-cpu.pb.gz').write_bytes(future.result())
            for service in services:
                (self.evidence/f'{variant}-{mode}-{service}-allocs-after.pb.gz').write_bytes(self.profile(service,'allocs'))
                (self.evidence/f'{variant}-{mode}-{service}-heap.pb.gz').write_bytes(self.profile(service,'heap'))
        time.sleep(2) # Let the bounded OTLP exporter flush, outside timing runs.

    def save(self):
        (self.evidence/'report.json').write_text(json.dumps(self.report,indent=2,ensure_ascii=False)+'\n')

    def execute(self):
        receiver=None
        self.trace_port=0
        try:
            self.boot()
            self.images={'baseline':(self.image,self.search_image)}
            if self.args.candidate:
                candidate=self.temporary/'candidate'
                candidate.mkdir()
                self.report['candidate_source']=frozen(self.args.candidate,candidate)
                self.images['candidate']=(self.image+'-candidate',self.search_image+'-candidate')
                for dockerfile,image in zip(('Dockerfile','Dockerfile.search'),self.images['candidate']):
                    with (self.evidence/(dockerfile+'.candidate-build.log')).open('w') as logfile:
                        lab.run(['docker','build','-f',dockerfile,'-t',image,'.'],cwd=candidate,stdout=logfile,timeout=600)
            self.seed()
            self.active_source=self.args.source
            if self.args.diagnostics:
                with socket.socket() as sock:
                    sock.bind(('0.0.0.0',0)); self.trace_port=sock.getsockname()[1]
                receiver=subprocess.Popen([str(lab.ROOT/'bin/perf-trace'),'--addr',f'0.0.0.0:{self.trace_port}',
                    '--output',str(self.evidence/'traces.jsonl')],stdout=subprocess.DEVNULL,stderr=(self.evidence/'trace-receiver.log').open('w'))
                self.diagnostics('baseline')
                if self.args.candidate: self.diagnostics('candidate')
            # Each label runs on exactly the same data and resource limits.
            order=('baseline','candidate','candidate','baseline') if self.args.candidate else ('baseline',)
            self.report['order']=list(order)
            for repeat,variant in enumerate(order):
                self.switch(variant)
                for mode in ('fulltext','basic'):
                    self.mode(mode)
                    self.load(f'{repeat+1}-{variant}-{mode}',mode)
            if lab.digest(self.catalog_rows()) != self.report['fixture_sha256']:
                raise ValueError('Catalog changed during the paired run')
            self.compose_run('start','search',log='final-recovery.log')
            self.refresh_address('search',18083)
            self.wait('final fulltext recovery',self.fulltext_ready)
            generation=self.request('search','/v1/status',token=self.environment['SEARCH_QUERY_TOKEN'])['generation']
            if generation != self.report['generation_before']:
                raise ValueError('Index generation changed during the paired run')
            self.report['dataset_and_generation_preserved']=True
            candidate_runs=[s for name,s in self.report['phases'].items() if 'candidate' in name and not name.startswith('diagnostic-')]
            candidate_pass=all(s['exit_code']==0 for s in candidate_runs)
            self.report['validation']={'candidate_runs':len(candidate_runs),'candidate_passed':candidate_pass,
                'baseline_failures_retained':True,'timing_profiles_and_traces':'disabled; separate diagnostic phases'}
            self.report['status']='recorded' if candidate_pass else 'candidate_failed'
            if not candidate_pass: raise ValueError('Candidate load checks failed; all evidence retained')
        except BaseException as error:
            self.report.update(status='failed',failure=str(error))
            raise
        finally:
            self.save()
            if receiver:
                receiver.terminate(); receiver.wait(timeout=5)
            for images in getattr(self,'images',{}).values():
                for image in images:
                    if image not in (getattr(self,'image',''),getattr(self,'search_image','')):
                        subprocess.run(['docker','image','rm','-f',image],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            self.cleanup()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source',type=Path,required=True)
    parser.add_argument('--candidate',type=Path)
    parser.add_argument('--jobs',type=int,choices=(1000,5000,10000),required=True)
    parser.add_argument('--seconds',type=int,default=30)
    parser.add_argument('--rate',type=int,default=8)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--diagnostics',action='store_true')
    args=parser.parse_args()
    if not (10<=args.seconds<=120 and 1<=args.rate<=100): parser.error('seconds 10..120; rate 1..100')
    # The reusable seed creates a separate writer. Count it in the exact scale.
    args.jobs-=1; args.revision=None; args.write_rate=1; args.max_vus=16
    args.source=args.source.resolve()
    if args.candidate: args.candidate=args.candidate.resolve()
    def interrupted(signum, frame): raise KeyboardInterrupt('Diagnostic lab interrupted')
    for event in (signal.SIGTERM,signal.SIGHUP): signal.signal(event,interrupted)
    # tmpfs database files are charged to the MySQL cgroup as well as its heap.
    # Keep this explicit, identical limit for every scale and both variants.
    lab.LIMITS = {**lab.LIMITS, 'mysql': (1, '1536m')}
    Diagnosis(args).execute()


if __name__=='__main__': main()
