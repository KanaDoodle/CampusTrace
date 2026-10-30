#!/bin/sh
# Disposable adversarial acceptance; no .env, existing volumes or external models.
set -eu
cd "$(dirname "$0")/.."
task_dir=$(mktemp -d /tmp/campustrace-adversarial.XXXXXX)
mysql_name="ct-adversarial-mysql-$$"
redis_name="ct-adversarial-redis-$$"
etcd_name="ct-adversarial-etcd-$$"
cleanup(){ docker rm -f "$mysql_name" "$redis_name" "$etcd_name" >/dev/null 2>&1 || true; rm -rf "$task_dir"; }
trap cleanup EXIT HUP INT TERM
export GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CAMPUS_INTEGRATION=1 CAMPUS_ADVERSARIAL=1 CAMPUS_LIVE_SOURCES=0
mkdir -p bin/verification/adversarial-20261030
export CAMPUS_ADVERSARIAL_EVIDENCE="${CAMPUS_ADVERSARIAL_EVIDENCE:-$PWD/bin/verification/adversarial-20261030/processes.jsonl}"
docker run --rm -d --name "$mysql_name" --tmpfs /var/lib/mysql -p 127.0.0.1::3306 -e MYSQL_ROOT_PASSWORD=synthetic-test-only -e MYSQL_DATABASE=campustrace_adversarial -e MYSQL_USER=synthetic -e MYSQL_PASSWORD=synthetic-test-only mysql:8.4 --mysqlx=OFF >/dev/null
docker run --rm -d --name "$redis_name" -p 127.0.0.1::6379 redis:7.4-alpine --save '' --appendonly no >/dev/null
docker run --rm -d --name "$etcd_name" -p 127.0.0.1::2379 quay.io/coreos/etcd:v3.6.7 etcd --data-dir=/tmp/test-etcd --listen-client-urls=http://0.0.0.0:2379 --advertise-client-urls=http://127.0.0.1:2379 >/dev/null
attempt=0
until docker exec "$mysql_name" mysql -h127.0.0.1 -usynthetic -psynthetic-test-only -e 'SELECT 1' campustrace_adversarial >/dev/null 2>&1 && docker exec "$etcd_name" etcdctl endpoint health --endpoints=http://127.0.0.1:2379 >/dev/null 2>&1; do
 attempt=$((attempt+1)); if [ "$attempt" -ge 60 ]; then echo 'Disposable dependencies not ready' >&2;exit 1;fi;sleep 1
done
mysql_port=$(docker port "$mysql_name" 3306/tcp | sed -n 's/^127.0.0.1://p')
redis_port=$(docker port "$redis_name" 6379/tcp | sed -n 's/^127.0.0.1://p')
etcd_port=$(docker port "$etcd_name" 2379/tcp | sed -n 's/^127.0.0.1://p')
export MYSQL_DSN="synthetic:synthetic-test-only@tcp(127.0.0.1:$mysql_port)/campustrace_adversarial?parseTime=true&loc=UTC"
export MYSQL_TEST_DSN="$MYSQL_DSN" REDIS_ADDR="127.0.0.1:$redis_port" ETCD_ENDPOINTS="127.0.0.1:$etcd_port"
go test -race -count=1 -v ./internal/integration -run "${ADVERSARIAL_TESTS:-Test(Adversarial|Scheduler|Application|Campaign|Durable|ExpiredTask|Whole|Company|MatchingV2|Decision|InventoryCampaign|MyRPC|RunningWorkerOutboxRPC|RepairAnalysisRegistrationReadiness)}"
go test -race -count=1 -v ./internal/pipeline ./internal/scheduling ./internal/recruitmentmail
