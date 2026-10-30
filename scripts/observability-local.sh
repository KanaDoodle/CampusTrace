#!/bin/sh
# Disposable full-stack acceptance. Never reads .env or uses business volumes.
set -eu
cd "$(dirname "$0")/.."
task_dir=$(mktemp -d /tmp/campustrace-observer.XXXXXX)
project="ct-observer-test-$$"
image="campustrace-observer-test:$$"
evidence="$PWD/bin/verification/observability-20261030"
mkdir -p "$evidence"
cleanup(){
 if test -f "$task_dir/compose.json"; then docker compose --project-name "$project" --file "$task_dir/compose.json" down --volumes --remove-orphans >/dev/null 2>&1 || true;fi
 docker image rm "$image" >/dev/null 2>&1 || true
 rm -rf "$task_dir"
}
trap cleanup EXIT HUP INT TERM
# Explicitly clear provider settings inherited by the shell; all data is synthetic.
export LLM_URL= LLM_API_KEY= LLM_MODEL= OTEL_TRACES_EXPORTER=otlp
export JWT_SECRET=synthetic-observer-test-secret-at-least-32
export CAMPUS_REDIS_PREFIX=test:observer: CAMPUS_DATABASE=campustrace
export OTEL_TRACES_SAMPLER_ARG=1
docker build --tag "$image" . > "$evidence/build.log" 2>&1
docker compose --env-file /dev/null --file docker-compose.yml --file compose.app.yml --file compose.observability.yml config --format json > "$task_dir/source.json"
python3 - "$task_dir/source.json" "$task_dir/compose.json" "$project" "$image" <<'PY'
import json,sys
source,dest,project,image=sys.argv[1:]
v=json.load(open(source));v['name']=project
for name,s in v['services'].items():
 s.pop('profiles',None)
 s.pop('ports',None)
 s['restart']='no'
 if name in ('api','worker','analysis-1','analysis-2','sandbox-runner'):
  s['image']=image;s.pop('build',None)
 if name=='mysql':s['volumes']=[];s['tmpfs']=['/var/lib/mysql']
 if name=='redis':s['volumes']=[];s['command']=['redis-server','--appendonly','no','--save','']
 if name=='etcd':s['volumes']=[];s['tmpfs']=['/etcd-data']
 if name in ('api','prometheus','tempo','grafana'):
  port={'api':8080,'prometheus':9090,'tempo':3200,'grafana':3000}[name]
  s['ports']=[{'target':port,'published':'0','host_ip':'127.0.0.1','protocol':'tcp'}]
# The practice runner is unrelated and must never mount the host Docker socket.
v['services'].pop('sandbox-runner',None)
for vol in v.get('volumes',{}).values():vol.pop('name',None)
for network in v.get('networks',{}).values():network.pop('name',None)
json.dump(v,open(dest,'w'),indent=2)
PY
if ! docker compose --project-name "$project" --file "$task_dir/compose.json" up -d --wait --wait-timeout 180 > "$evidence/startup.log" 2>&1;then
 docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
 cat "$evidence/startup.log" >&2
 exit 1
fi
for service in api prometheus tempo grafana;do
 case "$service" in api)port=8080;;prometheus)port=9090;;tempo)port=3200;;grafana)port=3000;;esac
 docker compose --project-name "$project" --file "$task_dir/compose.json" port "$service" "$port" > "$task_dir/$service.address"
done
python3 scripts/smoke-observability.py "$task_dir" "$evidence" "$project" > "$evidence/smoke.log" 2>&1 || {
 docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
 cat "$evidence/smoke.log" >&2
 exit 1
}
docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
cat "$evidence/smoke.log"
