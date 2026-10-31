#!/bin/sh
# Disposable full-stack acceptance. Never reads .env or uses business volumes.
set -eu
cd "$(dirname "$0")/.."
task_dir=$(mktemp -d /tmp/campustrace-observer.XXXXXX)
project="ct-observer-test-$$"
image="campustrace-observer-test:$$"
evidence="$PWD/bin/verification/observability-20261030"
if [ "${CAMPUS_SMOKE_KAFKA:-0}" = 1 ];then export CAMPUS_SMOKE_SEARCH=1;fi
if [ "${CAMPUS_SMOKE_SEARCH:-0}" = 1 ];then evidence="$PWD/bin/verification/search-20261030/stack";fi
if [ "${CAMPUS_SMOKE_KAFKA:-0}" = 1 ];then evidence="$PWD/bin/verification/kafka-20261031/stack";fi
search_image="$image-search"
mkdir -p "$evidence"
cleanup(){
 if test -f "$task_dir/compose.json"; then docker compose --project-name "$project" --file "$task_dir/compose.json" down --volumes --remove-orphans >/dev/null 2>&1 || true;fi
 docker image rm "$image" "$search_image" >/dev/null 2>&1 || true
 rm -rf "$task_dir"
}
trap cleanup EXIT HUP INT TERM
# Explicitly clear provider settings inherited by the shell; all data is synthetic.
export LLM_URL= LLM_API_KEY= LLM_MODEL= OTEL_TRACES_EXPORTER=otlp
export JWT_SECRET=synthetic-observer-test-secret-at-least-32
export CAMPUS_REDIS_PREFIX=test:observer: CAMPUS_DATABASE=campustrace
export OTEL_TRACES_SAMPLER_ARG=1
docker build --tag "$image" . > "$evidence/build.log" 2>&1
if [ "${CAMPUS_SMOKE_SEARCH:-0}" = 1 ];then
 docker build --file Dockerfile.search --tag "$search_image" . > "$evidence/search-build.log" 2>&1
 if [ "${CAMPUS_SMOKE_KAFKA:-0}" = 1 ];then
  docker compose --env-file /dev/null --file docker-compose.yml --file compose.app.yml --file compose.search.yml --file compose.kafka.yml --file compose.observability.yml --file compose.search-observability.yml --file compose.kafka-observability.yml config --format json > "$task_dir/source.json"
 else
 docker compose --env-file /dev/null --file docker-compose.yml --file compose.app.yml --file compose.search.yml --file compose.observability.yml --file compose.search-observability.yml config --format json > "$task_dir/source.json"
 fi
else
 docker compose --env-file /dev/null --file docker-compose.yml --file compose.app.yml --file compose.observability.yml config --format json > "$task_dir/source.json"
fi
python3 - "$task_dir/source.json" "$task_dir/compose.json" "$project" "$image" <<'PY'
import json,sys
source,dest,project,image=sys.argv[1:]
v=json.load(open(source));v['name']=project
for name,s in v['services'].items():
 s.pop('profiles',None)
 s.pop('ports',None)
 s['restart']='no'
 if name in ('api','worker','analysis-1','analysis-2','sandbox-runner','notify'):
  s['image']=image;s.pop('build',None)
 if name=='search':s['image']=image+'-search';s.pop('build',None)
 if name=='opensearch':s['volumes']=[];s['tmpfs']=['/usr/share/opensearch/data:uid=1000,gid=1000']
 if name=='kafka':s['volumes']=[];s['tmpfs']=['/var/lib/kafka/data:uid=1000,gid=1000']
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
# One-shot topic initialization is intentionally not a long-running health target.
services=$(python3 -c 'import json,sys;print(" ".join(n for n in json.load(open(sys.argv[1]))["services"] if n!="kafka-init"))' "$task_dir/compose.json")
if [ "${CAMPUS_SMOKE_KAFKA:-0}" = 1 ];then
 docker compose --project-name "$project" --file "$task_dir/compose.json" run --rm kafka-init > "$evidence/topic-init.log" 2>&1
fi
if ! docker compose --project-name "$project" --file "$task_dir/compose.json" up -d --wait --wait-timeout 180 $services > "$evidence/startup.log" 2>&1;then
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
if [ "${CAMPUS_SMOKE_SEARCH:-0}" = 1 ];then
 python3 scripts/smoke-search.py "$task_dir" "$evidence" "$project" > "$evidence/search-smoke.log" 2>&1 || {
  docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
  cat "$evidence/search-smoke.log" >&2
  exit 1
 }
 cat "$evidence/search-smoke.log"
fi
if [ "${CAMPUS_SMOKE_KAFKA:-0}" = 1 ];then
 python3 scripts/smoke-kafka.py "$task_dir" "$evidence" "$project" > "$evidence/kafka-smoke.log" 2>&1 || {
  docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
  cat "$evidence/kafka-smoke.log" >&2
  exit 1
 }
 cat "$evidence/kafka-smoke.log"
fi
docker compose --project-name "$project" --file "$task_dir/compose.json" logs --no-color > "$evidence/services.log" 2>&1
cat "$evidence/smoke.log"
